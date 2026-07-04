# databasemanager — Architecture (as-built)

**Status:** Describes the system after the 2026-07 structural refactor, which executed the
defect register (§10) produced by the architecture audit of commit `d60754c`. Every register
entry carries its outcome; the handful of deliberate behavior changes are listed in §12.

---

## 1. System Overview

Single-shot CLI. Provisions isolated PostgreSQL/MySQL databases and users, applies
privilege hardening (Postgres only), records the resulting credentials as secrets in
Infisical, and reads them back to list secrets, emit connection strings, and test
connectivity. Deletion resolves the app's recorded identity from Infisical, drops the
database and user, and removes the app's secrets and folder.

**Process model:** one process per invocation, single goroutine. The root context comes from
`signal.NotifyContext(SIGINT, SIGTERM)` in `main.go` and flows through
`ExecuteContext` into every command; interruption cancels in-flight SQL, and both engines'
provision rollback runs on a fresh 10s-timeout context so cleanup survives the cancelled
request context. Engine adapters own their connection lifecycle: lazy dial via
`ensureConnection`, released via `Close()` (deferred in the constructing command).

## 2. Composition & Wiring

One composition root, two stages, both under `main`'s control:

1. **`main.go`:** signal context → logger (Info) → empty `app.Container{Logger}` →
   `cmd.NewRootCmd(container)` → `ExecuteContext(ctx)`.
2. **`cmd/root.go` `PersistentPreRunE`** (after Cobra has parsed flags, so `--config` in
   both forms and `--verbose` actually work): raises the log level for `--verbose`, loads
   config via `config.Load(--config)`, constructs the Infisical client with the command
   context. Infisical failure is **warn-and-continue**: `Container.Infisical` stays nil and
   each consumer decides — `list`/`conn`/`test`/`delete` hard-fail, `provision` proceeds
   offline and reports truthfully (§5.1).

`app.Container{Config, Logger, Infisical}` is populated once and never mutated afterwards.
Per-invocation dependencies (the engine adapter) are constructed inside the command that
needs them via `database.New(engine, engineCfg)` and closed there.

## 3. Package Map

| Package | Responsibility | Notes |
|---|---|---|
| `main` | Composition root stage 1 | Signal context, logger, container shell |
| `cmd` | Cobra transport + composition root stage 2 | Owns flags, prompts, rendering (tables/JSON/masking), and the `ConnectionStringGenerator`; consumes domain types from `internal/database` |
| `internal/app` | Immutable dependency carrier | Three fields; no `DB` slot; not imported below `cmd` |
| `internal/services` | Provision/deprovision lifecycle | `Provisioner` with explicit `ProvisionerDeps`; owns password generation (`password.go`) |
| `internal/database` | Engine registry + adapters + domain types | `Database` interface, `New` factory, `ProvisionOptions.Validate`, `Credentials` (the secret contract), `TestAppConnection` |
| `internal/infisical` | Client construction + Universal Auth login | Takes `context.Context`; SDK's `InfisicalClientInterface` is the abstraction used everywhere |
| `internal/config` | Viper load + validation | Explicit `BindEnv` for every key: full env-only operation works; `Engine(name)` maps engine name → section |
| `internal/logger` | charmbracelet/log construction, stderr | Level raised by `--verbose` in `PersistentPreRunE` |

**Import direction:** `cmd → {app, services, database, config, infisical}`;
`services → {database, config, infisical SDK, log}`; `database → config`;
`app → {config, infisical SDK, log}`. `internal/app` is a leaf of `cmd` only.

## 4. Command Verticals

All five commands share one architecture: resolve dependencies at the top of `RunE`,
delegate to `internal/services` / `internal/database`, render in `cmd`.

| Command | Path |
|---|---|
| `provision` | `database.New(--type, cfg.Engine(--type))` → `services.Provisioner.Run` → sync or offline credential print |
| `delete` | `Provisioner.ResolveApp` (Infisical is the source of truth) → engine from `DB_TYPE` (`--type` only as legacy fallback) → confirmation prompt (`--force` skips) → `Provisioner.Deprovision` |
| `list` | Infisical SDK → table/JSON rendering; status via logger |
| `conn` | `fetchAppCredentials` → `Credentials.ResolveType/ValidateContract` → `ConnectionStringGenerator` → mask/copy/print |
| `test` | `fetchAppCredentials` → `Credentials.ResolveType` → `database.TestAppConnection(ctx, creds)` — no adapter constructed |

## 5. Data Flow

### 5.1 Provision
```
cmd/provision.go RunE
  ├─ engineCfg, _ := cfg.Engine(--type); db := database.New(--type, engineCfg); defer db.Close()
  └─ services.Provisioner{DB, Secrets, Logger, ProjectID, Engine, EngineCfg}.Run(ctx, req)
       ├─ prepareOptions   names sanitized (- → _) for BOTH db name and user;
       │                   password generated (crypto/rand) unless overridden;
       │                   recorded host/port sourced from EngineCfg — the same
       │                   section the adapter dials with; --schema rejected for mysql
       ├─ DB.Provision(ctx, opts)          both engines: Validate → existence check →
       │                                   create → rollback state machine on failure
       └─ syncToInfisical                  ensureFolder (fail-closed: create error tolerated
                                           only if List confirms existence) → batch write
                                           of Credentials.ToSecrets()
Result: Synced=true → success log.  Secrets nil → Warn + print credentials to stdout
(the only copy of the generated password).  Sync error → error log naming the orphaned DB.
```

### 5.2 Read paths (`list`, `conn`, `test`)
```
cmd/*.go RunE → Infisical.Secrets().List(/{app}, env) → database.ParseCredentials
   conn: ResolveType → ValidateContract → ConnectionStringGenerator → mask/copy/print
   test: ResolveType → database.TestAppConnection(ctx, creds)   (free function, app creds)
```

### 5.3 Delete
```
cmd/delete.go RunE
  ├─ Provisioner.ResolveApp(app, env)      recorded DB_NAME/DB_USER from Infisical;
  │                                        errors if app absent or names missing
  ├─ engine := creds.Type, else --type     mismatch between the two is an error
  ├─ confirmation prompt (stderr/stdin)    unless --force
  └─ Provisioner.Deprovision(ctx, app, env, creds)
       DB.Delete first (fail-closed on both engines) → delete each secret → delete folder;
       any post-drop failure is prefixed "database deleted, but failed to …" so partial
       state is visible and the remaining secrets keep the app resolvable.
```

## 6. Contracts

### 6.1 The secret contract — owned by `database.Credentials`

Written per app to Infisical at `/{appName}` in environment `--env`. The single definition
lives in `internal/database/credentials.go`: key constants (`DB_TYPE`, `DB_NAME`, `DB_USER`,
`DB_PASSWORD`, `DB_HOST`, `DB_PORT`, `DB_SCHEMA`), the `Credentials` struct, and its three
operations:

- `ToSecrets()` — serialization, deterministic order, `DB_SCHEMA` omitted when empty
  (writer: `services.Provisioner.syncToInfisical`).
- `ParseCredentials(map)` — the only deserializer (readers: `conn`, `test`, `delete`).
- `ResolveType()` / `ValidateContract(dbType)` / `requireCore()` — validation with the
  original user-facing diagnostics ("re-provision" hint, postgres-requires-schema /
  mysql-rejects-schema).

Renaming a key is now one edit with compiler support. Recorded `DB_HOST`/`DB_PORT` come
from the `DatabaseConfig` the adapter dialed (`ProvisionerDeps.EngineCfg`), so stored
secrets match reality on non-default ports.

### 6.2 Naming & default derivation

| Value | Rule | Owner |
|---|---|---|
| DB name | `{app with - → _}_db`, overridable via `--db` | `services.prepareOptions` |
| DB user | `{app with - → _}_user`, overridable via `--user` (same sanitizer as db name) | same |
| Password | 32-hex crypto/rand, overridable via `--pass` | `services/password.go` |
| Port / Host | `--port`/`--host` override, else `EngineCfg` (the dialed section) | same |
| Schema | `--schema` (postgres only, rejected otherwise), default `public` | same |

Delete never derives names: it uses the recorded `DB_NAME`/`DB_USER` from Infisical.

### 6.3 The engine registry — single owner

`internal/database/factory.go`: `EnginePostgres`/`EngineMySQL` constants,
`engineFactories` map, `Engines()` (sorted, used in error messages), and
`New(engine, cfg)` which fails fast on unknown engines and unconfigured sections. Adding
an engine = one map entry + one `Database` implementation (+ a `config.Engine` case and a
`ConnectionStringGenerator` branch for `conn`).

### 6.4 Configuration

Sources: YAML file (`--config` path, else `$HOME` or `.`, `.databasemanager.yaml`), then
environment (`DATABASEMANAGER_*`). Every key in `envBoundKeys()` is explicitly
`BindEnv`-ed, so env-only operation (no file at all) works and is pinned by
`TestLoad_EnvOnly`. Validation requires the three Infisical fields and completeness of any
engine section that sets a hostname; a config with **no** engine section is valid by design
(`list`/`conn`/`test` only need Infisical) — commands that need an engine get the error
from `database.New`.

## 7. Engine Adapter Semantics (converged)

| Concern | PostgresClient | MySQLClient |
|---|---|---|
| Pre-existence check | `pg_database` lookup, parameterized | `INFORMATION_SCHEMA` lookup, parameterized |
| Provision rollback | state machine, fresh 10s context | state machine, fresh 10s context |
| Delete error policy | fail-closed, every step checked | fail-closed, every step checked |
| Hardening | REVOKE PUBLIC connect (checked), reown `public`, grant/create schema | GRANT ALL scoped to the one database |
| Admin dial | one path (`ensureConnection` + `dsn()`), configured db, `sslmode=disable` | one path, no db selected, 10s timeout |
| Quoting | `pq.QuoteIdentifier` + `pq.QuoteLiteral` (password, kill-query db name) | backtick-doubling for identifiers, `escapeMySQLLiteral` (backslash+quote) for literals |
| Identifier gate | `ProvisionOptions.Validate()`: `^[A-Za-z_][A-Za-z0-9_]*$`, ≤63 chars (≤32 for users), non-empty password — enforced before any SQL is built | same (shared) |

The `Database` interface is exactly what consumers call: `Provision`, `Delete`, `Close`.
App-credential connectivity testing is the free function `database.TestAppConnection`.

## 8. Output & Observability

- Logger (charmbracelet) writes to **stderr**; `--verbose` raises it to Debug.
- **stdout is data only**: tables, JSON, connection strings, and the offline-provision
  credential dump. All status/"Fetching…" chrome goes through the logger, so
  `--format json` pipes cleanly.
- Masking policy is in `cmd`: key-substring masking for `list`
  (`PASSWORD|KEY|SECRET|TOKEN`), full/middle password masking for `conn` output.

## 9. Testing

`make test` runs `go test ./...`. Coverage by package:

- `cmd`: pin tests for the pure functions — `ConnectionStringGenerator` (all formats,
  schema variants, dispatch, `GenerateAll`), password masking (full/middle/string), and
  `maskSecretValue`.
- `internal/database`: `ProvisionOptions.Validate` matrix, `Credentials` round-trip /
  ordering / `ResolveType` / `ValidateContract` matrices, factory (unknown engine,
  unconfigured engine, construction), `escapeMySQLLiteral`, `TestAppConnection` input
  rejection. SQL execution paths still require a live server (not covered).
- `internal/services`: `Provisioner` via `MockDB` — defaults (incl. sanitized user +
  truthful `Synced=false` offline), engine-config-sourced facts (port 5433 pin), overrides,
  mysql-schema rejection, `sanitizeAppName`, password generation.
- `internal/config`: file/env matrices including env-only loading.

Not covered: Infisical-backed flows (`ResolveApp`, `Deprovision`, folder ensure) — the SDK
interface fans out into many sub-interfaces; verified by review and smoke tests instead.

## 10. Defect / Drift Register — outcomes

All 32 entries executed 2026-07-04. Line references are to the post-refactor tree.

| ID | Defect (abridged) | Outcome |
|---|---|---|
| D1 | Delete derived names by convention, diverging from provision | **Fixed** — delete resolves recorded `DB_NAME`/`DB_USER` via `Provisioner.ResolveApp`; provision sanitizes db name AND user identically (`services/provisioner.go:88-90`) |
| D2 | Delete lost Infisical cleanup, confirmation, `--force`, `--env` | **Fixed** — `Provisioner.Deprovision` (DB → secrets → folder, partial failures surfaced); prompt + `--force` + `--env` restored (`cmd/delete.go`) |
| D3 | Recorded `DB_PORT` hardcoded 5432/3306 | **Fixed** — port/host come from `ProvisionerDeps.EngineCfg`; pinned by `TestProvisioner_Run_RecordsEngineConfigFacts` |
| D4 | Dead `localhost` default / empty host propagation | **Fixed** — same source as D3; `database.New` rejects unconfigured engines before any dial |
| D5 | "Secrets synced" logged unconditionally; offline password lost | **Fixed** — `ProvisionResult.Synced`; offline path warns and prints the credentials to stdout (`cmd/provision.go:72-79`) |
| D6 | `--config=path` silently ignored (hand-scanned `os.Args`) | **Fixed** — read via Cobra in `PersistentPreRunE`; both forms smoke-tested |
| D7 | `--verbose` parsed and discarded | **Fixed** — raises logger to Debug in `PersistentPreRunE` |
| D8 | No context/signal handling | **Fixed** — `signal.NotifyContext` + `ExecuteContext` (`main.go`); rollback contexts are fresh so cleanup survives cancellation |
| D9 | Mutable `Container.DB` service-locator slot | **Fixed** — field deleted; adapters constructed and closed inside commands |
| D10 | Secret contract as `map[string]string` across 4 packages | **Fixed** — `database.Credentials` owns keys, serialization, parsing, validation (`credentials.go`) |
| D11 | `ProvisionOptions.Validate()` unconditional nil | **Fixed** — identifier allowlist + length caps + password presence; called by both adapters before SQL construction |
| D12 | Raw string-literal interpolation into SQL | **Fixed** — `pq.QuoteLiteral` (postgres password, kill-query), `escapeMySQLLiteral` (mysql literals), identifier gate for names; parameterized existence checks |
| D13 | MySQL Delete discarded all errors | **Fixed** — fail-closed, each step checked (`mysql.go:167-182`) |
| D14 | REVOKE CONNECT error shadowed | **Fixed** — checked (`postgres.go:146-148`) |
| D15 | Existence check + rollback on MySQL only | **Fixed** — both engines share the pattern (`provisionState`, deferred `rollbackProvision`) |
| D16 | Two MySQL dial paths | **Fixed** — single `ensureConnection` (no db, 10s timeout); `Connect` removed |
| D17 | Interface wider than consumers (`Connect`/`Test` unused) | **Fixed** — interface is `Provision/Delete/Close`; `TestAppConnection` is a free function on `Credentials` |
| D18 | Service depends on whole `app.Container` | **Fixed** — explicit `ProvisionerDeps{DB, Secrets, Logger, ProjectID, Engine, EngineCfg}`; `internal/app` no longer imported below `cmd` |
| D19 | Zero-engine config accepted via empty block + TODO | **Fixed by decision** — zero-engine config is *valid* (read-only commands need no engine); the dead block is gone and engine absence errors surface in `database.New` with actionable text |
| D20 | Env overrides only for file-present keys | **Fixed** — explicit `BindEnv` over `envBoundKeys()`; env-only config pinned by test |
| D21 | Domain logic homed in `cmd/conn.go`, shared by import | **Fixed** — contract detection/validation moved to `database.Credentials`; generation/masking/rendering remain in `cmd` as presentation |
| D22 | Status chrome on stdout corrupts `--format json` | **Fixed** — all "Fetching…" lines go through the logger (stderr) |
| D23 | Engine registry triplicated across commands | **Fixed** — single `engineFactories` registry + `database.New`; the three `RunE` switches deleted |
| D24 | `internal/utils` catch-all | **Fixed** — deleted; password generation is `services/password.go` (unexported) |
| D25 | `make test` excluded `cmd/` | **Fixed** — `go test ./...` |
| D26 | Live credentials committed (`.databasemanager.yaml.backup`) | **Mitigated** — untracked (`git rm --cached`) + `.gitignore` pattern `.databasemanager.yaml*`. **Still required:** rotate the Infisical client secret and DB password (they remain in git history); rewriting history is a repo-owner decision |
| D27 | `--schema`/`--port` unreachable; dead custom-schema branch | **Fixed** — `--schema`, `--port`, `--host` flags restored on provision; custom-schema branch in `lockdownDatabase` is live again |
| D28 | Folder-create failures demoted to Debug | **Fixed** — fail-closed: create error tolerated only when `Folders().List` confirms prior existence |
| D29 | Admin pools never closed | **Fixed** — `Close()` on the interface, deferred by constructing commands |
| D30 | Speculative mutexes in a single-goroutine process | **Fixed** — removed |
| D31 | Infisical SDK on `context.Background()` | **Fixed** — `infisical.NewClient(ctx, cfg)` receives the command context |
| D32 | `GenerateAll` discarded generation errors | **Fixed** — errors propagate; unknown type is an error, not an empty table |

## 11. Boundary Invariants (now in force — keep them)

1. `app.Container` is immutable after `PersistentPreRunE`; it never grows a mutable slot.
2. Services receive explicit dependencies (`ProvisionerDeps`), never the container;
   `internal/app` is imported only by `cmd` and `main`.
3. The secret contract has exactly one owner: `database.Credentials`. No
   `map[string]string` credential plumbing outside `ParseCredentials`.
4. Delete resolves identity from Infisical, never from naming convention.
5. Recorded connection facts are the dialed connection facts (`EngineCfg` is the single
   source for both).
6. One composition root; flags are read only through Cobra after parse.
7. Both engine adapters honor the same Provision guarantee (validate → existence check →
   rollback on failure) and fail-closed Delete.
8. Every SQL identifier passes `ProvisionOptions.Validate`; every literal goes through the
   engine's quoting/escaping helper. No raw interpolation.
9. stdout is data; stderr (logger) is chrome.
10. `make test` covers `./...`; behavior-bearing pure functions keep their pin tests.

## 12. Deliberate Behavior Changes (vs `d60754c`)

Everything else is behavior-preserving (error strings, DSN formats, masking, table output).
These are the intentional deltas, all traceable to register entries:

- `delete` now resolves from Infisical, prompts for confirmation (`--force` to skip),
  cleans up secrets + folder, and takes `--env`; its `--type` flag defaults to empty and is
  only a fallback for legacy apps missing `DB_TYPE` (D1, D2).
- `provision` gained back `--schema`, `--port`, `--host`; `--schema` on a non-postgres
  engine is rejected instead of silently recording a contract-violating `DB_SCHEMA` (D27).
- Offline provisioning (Infisical unreachable) prints the credentials instead of falsely
  claiming a sync (D5).
- Empty-string secret values are now equivalent to absent ones (`ParseCredentials` drops
  the map-presence distinction): e.g. a mysql app with an empty `DB_SCHEMA` value no longer
  fails contract validation.
- `conn --format table` prints "Detected database type" via the logger (stderr), not
  stdout; `list`'s status lines likewise (D22).
- Interrupts (Ctrl-C / SIGTERM) now cancel in-flight work (D8).
