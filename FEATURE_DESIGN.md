# Feature Design — `rotate` and idempotent provisioning (`--adopt`)

**Status:** Phases 0–3 **implemented** on branch `feat/rotate-and-adopt`
(2026-07-07), one commit per phase; Phase 4 (build-tagged `testcontainers-go`
integration suite) remains open. The `secretStore` port recommended in §1.6 was
pulled into Phase 1 as designed. Written 2026-07-07 against the post-hardening
tree (branch `harden/tls-injection-masking`).
Companion to [`ARCHITECTURE.md`](ARCHITECTURE.md) (invariants referenced as §11.*) and
[`README.md`](README.md) (gap table this design executes).

Two features, one document, because they share their hardest parts:

1. **`rotate <app>`** — regenerate an app's password, apply it to the database user,
   update `DB_PASSWORD` in Infisical, verify.
2. **`provision --adopt`** — make provisioning convergent: tolerate pre-existing
   database/user, upsert (not create-only) the secret sync, and thereby give every
   partial-failure state a one-command recovery.

---

## 0. Shared foundation

Both features reduce to the same two primitives, which is why they should be built
together and in this order:

| Primitive | Used by | New code |
|---|---|---|
| **Apply a known password to an existing user** | `rotate` (its whole point); `--adopt` (a pre-existing user's password is unknowable — password hashes can't be read back — so adoption *must* set a fresh one) | `Database.RotatePassword` |
| **Write the secret contract over possibly-existing secrets** | `--adopt` (its whole point); `rotate` (updating `DB_PASSWORD`, incl. re-creating it if it was manually deleted) | `planSecretSync` + per-key SDK calls |

Plus two smaller refactors both features want:

- **`resolveEngine(creds, typeFlag)` helper** — `delete` already implements the
  "stored `DB_TYPE` is authoritative, `--type` is a legacy fallback, mismatch is an
  error" dance inline (`cmd/delete.go:45-54`). `rotate` needs the identical logic.
  Factor it into a shared `cmd` helper rather than copying a third variant.
- **`confirmAction(cmd, message) bool` helper** — the stderr-prompt/stdin-read block in
  `cmd/delete.go:64-74` is needed verbatim by `rotate` and `provision --adopt`.

### 0.1 SDK facts this design rests on (verified against `go-sdk@v0.5.100`)

| Capability | Available | Where |
|---|---|---|
| Per-key update | ✔ `Secrets().Update(UpdateSecretOptions{SecretKey, ProjectID, Environment, SecretPath, NewSecretValue})` — HTTP `PATCH /v3/secrets/raw/{key}` | `secrets.go:29`, `packages/api/secrets/update_secret.go` |
| Per-key create | ✔ `Secrets().Create(CreateSecretOptions{…})` | `secrets.go` |
| Per-key delete | ✔ `Secrets().Delete(…)` (already used by `Deprovision`) | `internal/services/provisioner.go:274` |
| Batch create | ✔ `Secrets().Batch().Create` (already used by `syncToInfisical`) | `internal/services/provisioner.go:180` |
| **Batch update / upsert** | ✘ **does not exist** — `BatchSecretsInterface` has exactly one method, `Create` | `secrets.go:22-24` |

**Consequence:** reconciliation cannot be one atomic API call. It must be a
*computed plan* of per-key creates/updates/deletes, executed in deterministic order,
where every step is idempotent so a partial failure is recoverable by re-running.
That plan computation is a pure function — the most testable piece of both features.

### 0.2 Suggested build order

```
Phase 0  Quick win: print credentials when sync fails (5 lines, ships alone)
Phase 1  Database.RotatePassword (both adapters) + rotate command
Phase 2  planSecretSync + upsert executor in the provisioner
Phase 3  provision --adopt (uses 1 + 2)
Phase 4  (optional, recommended) secretStore port + integration tests
```

---

## 1. Feature: `rotate`

### 1.1 Problem statement

There is no way to change a provisioned app's password with this tool. Today an
operator must, by hand: `ALTER USER … WITH PASSWORD …` as admin, then edit
`DB_PASSWORD` in the Infisical UI, then hope the two match. Two secrets stores
mutated manually, no verification, no audit trail in the tool's own terms.

Rotation is also the **recovery story** for two situations the tool can create
itself:

- Offline provisioning printed the credentials and the operator lost them.
- `DB_PASSWORD` was deleted or corrupted in Infisical.

In both cases the password is unrecoverable (only hashes exist server-side), but a
*new* one can be issued — which is exactly what rotate does.

### 1.2 Proposed UX

```
databasemanager rotate <app-name> [flags]
```

| Flag | Type | Default | Detail |
|---|---|---|---|
| `--env` | string | `dev` | Infisical environment the app lives in. |
| `--pass` | string | *(generated)* | Override the new password. Default: 32-hex `crypto/rand`, same generator as provision. Must be non-empty. |
| `--type` | string | *(empty)* | Legacy fallback for apps missing `DB_TYPE`, identical semantics to `delete --type` (mismatch with a stored type is an error). |
| `--force` | bool | `false` | Skip the confirmation prompt. |
| `--no-verify` | bool | `false` | Skip the post-rotation connectivity check. |

**Confirmation prompt** (rotation is disruptive — see §1.5):

```
About to rotate the password for user "billing_api_user" (app "billing-api",
database "billing_api_db", environment "dev").
Apps holding the current password will fail to authenticate on their next (re)connect.
Type 'y' or 'yes' to confirm:
```

**Success output** — the new password is *never* printed (it lives in Infisical,
retrievable via `list <app> DB_PASSWORD --show-values`):

```
INFO  rotating password app=billing-api type=postgres env=dev
INFO  password applied to database user user=billing_api_user
INFO  DB_PASSWORD updated in Infisical
INFO  verified: app credentials connect successfully
```

### 1.3 The core design problem: two-resource consistency

The database's password and Infisical's `DB_PASSWORD` must change together, and
there is no transaction spanning them. One of them changes first; the process can
die or fail between the two. The design question is which order fails better.

**Option A — database first, then Infisical (chosen).**

Failure window: DB accepts the *new* password, Infisical still serves the *old* one.

- The blast radius is bounded: apps holding the old password lose auth on
  reconnect — but that is true of *any* rotation, in any order, by definition.
- Recovery is automatic: we still hold the old password (we just read it via
  `ResolveApp`), so on a failed Infisical update the service **rolls the database
  back to the old password** — same philosophy as provisioning's rollback state
  machine, on a fresh 10-second context (§11.7 pattern, `postgres.go:146`). After a
  successful rollback, *nothing changed*; the operator re-runs later.
- If the rollback itself also fails (DB unreachable mid-operation), the tool prints
  the **new** password to stdout as the operator's only copy — mirroring the
  offline-provisioning precedent (`cmd/provision.go:73-79`) — and exits 1 with an
  error naming the exact partial state.

**Option B — Infisical first, then database (rejected).**

Failure window: Infisical serves a password the database does not accept yet. That
window's blast radius extends beyond this CLI: *every consumer* that reads secrets
during it (app restarts, sidecar refreshes) fetches a dead credential. Option A's
window only affects consumers that were going to be re-authenticated anyway. B also
breaks the codebase's existing precedent that the database mutation happens first
and partial states are named from the DB's perspective ("database was created but
its secrets were not synced", `provisioner.go:81`).

**Failure matrix (Option A as designed):**

| Step that fails | State afterwards | Automatic action | Operator action |
|---|---|---|---|
| Resolve / validate | Nothing changed | — | Fix input, re-run |
| `ALTER USER` (DB) | Nothing changed | — | Re-run |
| Infisical update | DB=new, Infisical=old | **Rollback `ALTER` to old** → nothing changed | Re-run when Infisical is healthy |
| Rollback after failed update | DB=new, Infisical=old | Print new password to stdout, exit 1 with explicit partial-state error | Update the secret manually *or* re-run `rotate` (see below) |
| Verification dial | Rotation fully applied | Warn/error distinguishing "rotation succeeded; verification failed" | Investigate reachability (see §1.5) |

**Self-healing property:** re-running `rotate` always converges. It reads whatever
Infisical has (even a stale password — irrelevant, the DB change runs as *admin*),
issues a fresh password, and overwrites both sides. There is no stuck state that a
re-run doesn't fix, which is the property that makes the last-resort row above
acceptable.

### 1.4 Required changes, layer by layer

#### (a) `internal/database/database.go` — widen the interface

```go
type Database interface {
    Provision(ctx context.Context, provisionOptions ProvisionOptions) error
    Delete(ctx context.Context, databaseName, userName string) error
    // RotatePassword sets a new password for an existing provisioned user.
    // userName arrives from Infisical and must be re-validated before any SQL.
    RotatePassword(ctx context.Context, userName, newPassword string) error
    Close() error
}
```

This respects invariant §11 ("the interface is exactly what consumers call") — the
rotate command and, later, the adopt path both call it. `MockDB` in
`provisioner_test.go` gains the method (the compiler enforces this).

#### (b) `internal/database/postgres.go`

```go
func (p *PostgresClient) RotatePassword(ctx context.Context, userName, newPassword string) error {
    // Same trust boundary as Delete: the name is Infisical-sourced.
    if err := validateIdentifier("database user", userName, maxUserLen); err != nil {
        return err
    }
    if newPassword == "" {
        return fmt.Errorf("new password must not be empty")
    }
    if err := p.ensureConnection(ctx); err != nil {
        return err
    }
    _, err := p.db.ExecContext(ctx,
        fmt.Sprintf("ALTER USER %s WITH PASSWORD %s",
            pq.QuoteIdentifier(userName), pq.QuoteLiteral(newPassword)))
    ...
}
```

Notes:
- `ALTER USER` affects only *future* authentications; live sessions keep running.
  That is desirable (no surprise outage) and must be documented (§1.5).
- Factor the statement construction into a small pure helper if you want a pin test
  without a live server (mirrors how `dsn()` is pinned today).

#### (c) `internal/database/mysql.go`

```go
func (m *MySQLClient) RotatePassword(ctx context.Context, userName, newPassword string) error {
    if err := validateIdentifier("database user", userName, maxUserLen); err != nil { … }
    if err := m.ensureConnection(ctx); err != nil { … }   // also learns sql_mode
    _, err := m.db.ExecContext(ctx,
        fmt.Sprintf("ALTER USER '%s'@'%s' IDENTIFIED BY '%s'",
            m.escapeLiteral(userName), m.escapeLiteral(m.userHost()), m.escapeLiteral(newPassword)))
    ...
}
```

Notes:
- Targets `'user'@'<database_user_host>'` — the **same stability caveat as delete**
  (ARCHITECTURE §6.4): if `database_user_host` changed since provisioning, the
  account doesn't exist under the current host and MySQL errors. The error message
  should hint at this (`…does the configured database_user_host still match the
  one used at provision time?`).
- `ALTER USER` requires no `FLUSH PRIVILEGES` (it's not a direct grant-table edit).
- Escaping goes through `escapeLiteral` — SQL-mode-aware, learned at connect
  (`mysql.go:113-119`), so `NO_BACKSLASH_ESCAPES` servers are handled.

#### (d) `internal/services/provisioner.go` — orchestration

```go
type RotateResult struct {
    Credentials database.Credentials // with the NEW password
    Synced      bool                 // DB_PASSWORD update landed in Infisical
    RolledBack  bool                 // sync failed and the DB was restored to the old password
}

func (p *Provisioner) Rotate(ctx context.Context, appName, environment, overridePassword string) (*RotateResult, error)
```

Flow:

1. `ResolveApp(appName, environment)` — existing method; source-of-truth
   resolution, same as delete. **Do not require the old `DB_PASSWORD` to be
   present** — `ResolveApp` only demands `DB_NAME`/`DB_USER` (`provisioner.go:244`),
   and a missing password secret is precisely the recovery case rotate should
   handle.
2. New password: `overridePassword`, else `generateRandomPassword(16)`.
3. `p.deps.DB.RotatePassword(ctx, creds.User, newPassword)`.
4. Update Infisical: `Secrets().Update(...)` on `DB_PASSWORD` at `/{app}`. If the
   update fails with not-found (secret was deleted manually), fall back to
   `Secrets().Create(...)`. (The prior `List` in `ResolveApp` already tells us
   which branch to expect; the fallback covers races.)
5. On step-4 failure **and** old password known: attempt
   `RotatePassword(freshCtx, creds.User, oldPassword)` on a fresh 10 s context;
   report `RolledBack` accordingly. If the old password was unknown (recovery
   case), there is nothing to roll back *to* — go straight to the
   print-and-exit-1 path.

Rotate has a **hard Infisical dependency** (unlike provision): identity resolution
and the whole point of the operation both need it. `nil` client → immediate error,
same as delete (`cmd/delete.go:29-31`).

#### (e) `cmd/rotate.go` — new command

Mirrors `delete`'s shape exactly (which is the argument for the two shared helpers
in §0):

1. `ExactArgs(1)`; nil-Infisical → error.
2. `ResolveApp` → `resolveEngine(creds, --type)` → `warnInsecureTLS` →
   `database.New(engine, engineCfg)` + `defer Close`.
3. Confirmation prompt (unless `--force`).
4. `provisioner.Rotate(...)`.
5. Unless `--no-verify`: `database.TestAppConnection(ctx, result.Credentials,
   engineCfg.DatabaseSSLMode)` — dials as the app with the new password.
6. Output per the rules in §1.2 / failure matrix in §1.3.

Register in `cmd/root.go` (`cmd.AddCommand(newRotateCmd(container))`).

### 1.5 Decisions & edge cases

| # | Decision | Recommendation & rationale |
|---|---|---|
| 1 | Verify by default? | **Yes, with `--no-verify` opt-out.** It catches real rotation bugs (wrong `database_user_host` scoping, sslmode mismatch) at the moment they're cheapest to fix. Caveat: `TestAppConnection` dials the *recorded* `DB_HOST:DB_PORT`, which can differ from the admin host when the app was provisioned with `--host` — the failure message must therefore distinguish "rotation applied; verification dial failed (possibly a reachability issue from this machine)" from a rotation failure. Exit 1 either way, but the message makes the state unambiguous. |
| 2 | Kill live sessions? | **No (v1).** Postgres/MySQL check passwords at connect time only; existing sessions survive rotation. This is the least-surprising default. A `--kill-sessions` flag (Postgres: the `pg_terminate_backend` query delete already uses, `postgres.go:221-227`; MySQL: iterate `information_schema.processlist`) is a clean follow-up if forced re-auth is wanted. Document the behavior in README. |
| 3 | Missing `DB_PASSWORD` secret | Supported deliberately (recovery use-case): update-falls-back-to-create. No rollback possible in this case (no old password); the last-resort print path covers a sync failure here. |
| 4 | Prompt shows the database name & user | Yes — and this is the moment to also fix the README nice-to-have "delete prompt doesn't show the host": include `DB_HOST` in *both* prompts. Wrong-cluster rotation is as damaging as wrong-cluster deletion. |
| 5 | `--pass` validation | Non-empty only. Charset is free — every downstream sink (DSN, URI, shell formats) quotes/encodes (invariant §11.12). |
| 6 | Concurrency | No locking, last-write-wins — consistent with the whole tool's single-operator model. Two concurrent rotations of one app leave both sides holding whichever write landed last *per side*; a re-run converges. Documented, not defended. |
| 7 | Legacy apps (no `DB_TYPE`) | Supported via `--type` fallback, same as delete — rotate is a maintenance operation and legacy apps are the ones most likely to need it. (Contrast `conn`/`test`, which hard-require `DB_TYPE`.) |

### 1.6 Test plan

| Test | What it pins |
|---|---|
| `TestPostgresRotate_RejectsBadIdentifiers` / `TestMySQLRotate_RejectsBadIdentifiers` | The identifier gate fires on the Infisical-sourced user name **before any dial** (client with no db handle), mirroring `TestPostgresDelete_RejectsBadIdentifiers` |
| `TestPostgresRotate_RejectsEmptyPassword` | Empty new password refused pre-dial |
| Statement-builder pins (if factored) | `ALTER USER "u" WITH PASSWORD 'p''q'` exact rendering; MySQL variant under both SQL modes |
| `TestProvisioner_Rotate_GeneratesPassword` | MockDB receives a 32-hex password; result credentials carry it; `Synced` truthful |
| `TestProvisioner_Rotate_HonoursOverride` | `--pass` value reaches the adapter verbatim |
| `TestProvisioner_Rotate_RequiresInfisical` | nil Secrets → error before any DB call |
| `TestProvisioner_Rotate_RollsBackOnSyncFailure` | On a failed secret update, the adapter sees a second `RotatePassword` with the *old* password |
| `MockDB.RotatePassword` | Added; interface change is compiler-enforced across the suite |

**The honest gap:** the rollback-on-sync-failure test needs a *failable* secrets
dependency, and today the provisioner depends on the SDK's wide
`InfisicalClientInterface`, which is why Infisical-backed flows are currently
untested (ARCHITECTURE §9). Since sync-failure handling **is rotate's core risk**,
this feature is the right moment to extract a narrow port:

```go
// internal/services — the five operations the provisioner actually uses.
type secretStore interface {
    ListSecrets(env, path string) (map[string]string, error)
    CreateSecret(env, path, key, value string) error
    UpdateSecret(env, path, key, value string) error
    DeleteSecret(env, path, key string) error
    EnsureFolder(env, name string) error
    DeleteFolder(env, name string) error
}
```

with one adapter wrapping the SDK. ~100 lines, and it makes `Rotate`, `Run`'s sync,
`ResolveApp`, and `Deprovision` all unit-testable with a 20-line fake. Listed as
Phase 4 but genuinely worth pulling into Phase 1.

### 1.7 Documentation & effort

- README: command reference entry, secret-contract note (`DB_PASSWORD` is mutable
  post-provision), remove the gap-table row.
- ARCHITECTURE: §4 verticals table (+1 row), §6 contracts, §7 adapter table
  (+RotatePassword row), §9 testing, §11 invariant 7 wording ("Provision/Delete/
  RotatePassword all validate identifiers…").
- HARDENING_PLAYBOOK: a §5-style unit-test fast path plus a live rotation demo.

**Effort:** ~150 LOC production + ~200 LOC tests without the port; +~120 LOC with
it. No new dependencies. One to two sessions.

---

## 2. Feature: idempotent provisioning (`provision --adopt`)

### 2.1 Problem statement — the three stuck states

Provisioning is strict create-only at both ends: the adapter errors if the database
exists (`postgres.go:104-110`, `mysql.go:144-150`), and the sync is
`Batch().Create` (`provisioner.go:180`), which errors if any contract key already
exists. Tracing the actual code paths, three stuck states arise, and their current
recovery is worse than the README's gap table suggested:

**State A — database created, sync failed** (Infisical down, wrong env slug, …).
`Run` returns the error *without printing the credentials* (`provisioner.go:80-83`;
only the nil-client path prints) — **the generated password is irretrievably
lost**. Worse, the advertised recovery ("delete and re-provision") doesn't work:
`delete` resolves the app from Infisical, and there are no secrets, so it fails
with `app not found` (`provisioner.go:234-236`). **Actual recovery today: manual
`DROP DATABASE` / `DROP USER` in psql/mysql as admin.**

**State B — deliberate offline provisioning** (`Synced=false`, credentials printed
to stdout). Works as designed, but there is no path to ever get those credentials
*into* Infisical afterwards: re-running provision dies at "database already
exists".

**State C — secrets exist, database doesn't** (DB dropped manually, server
restored from backup, engine wiped in dev). Re-running provision makes it *worse*:
the DB creates fine, then `ensureFolder` tolerates the existing folder, then
`Batch().Create` fails on the existing keys — producing **State A again, plus
stale secrets** whose `DB_PASSWORD` matches neither the old nor the new database.

**Quick win (Phase 0, independent of everything else):** on sync failure, print
the credentials exactly like the offline path does. Five lines in
`cmd/provision.go` / `Run`, and State A stops eating passwords. Ship this first.

### 2.2 Proposed UX

```
databasemanager provision <app-name> --adopt [--pass <known-password>] [--force] [other provision flags]
```

`--adopt` changes provisioning from *create-or-die* to **converge-to-desired-state**:

| Resource | Absent | Present |
|---|---|---|
| Database | create (as today) | **adopt** — skip creation, keep data |
| User | create (as today) | **adopt** — keep, but apply a known password (see below) |
| Password | generate (or `--pass`) | *unknowable* (hashes only) → **always set a fresh generated one, or `--pass` if given** — adoption is implicitly a rotation |
| Hardening (pg) / grants (mysql) | apply | **re-apply idempotently** |
| Secrets | create | **upsert**: update existing contract keys, create missing ones, delete contract keys that shouldn't exist (e.g. stale `DB_SCHEMA`), leave non-contract keys in the folder untouched |

**Why one flag and not `--adopt` + `--resync`:** a secrets-only "resync" is
meaningless without a password to record, and passwords can't be read back from
the engine — so a truthful resync must either take `--pass` (operator captured it
from offline output: State B) or set a fresh one (States A/C). Both are exactly
`--adopt` with all resources already existing. One flag, one semantics:
*"after this command, the database, user, password, hardening, and secrets are
consistent — whatever was there before."*

**Confirmation prompt** (unless `--force`) — adoption mutates things the run didn't
create, so it must say precisely what it found and what it will do:

```
Adopting app "billing-api" (postgres, env "dev") on db.internal:5432:
  database "billing_api_db":  EXISTS — will adopt (data preserved, hardening re-applied)
  user     "billing_api_user": EXISTS — will adopt; password will be RESET
  secrets  /billing-api:       6 keys exist — will be updated in place
Apps using the current password will lose access on next reconnect.
Type 'y' or 'yes' to confirm:
```

**Recovery walkthroughs:**

```bash
# State A (sync failed, password lost):
databasemanager provision billing-api --adopt
#   → db+user adopted, fresh password ALTERed in, all 6 secrets created. Healed.

# State B (offline provisioning, creds captured from stdout):
databasemanager provision billing-api --adopt --pass '<captured>'
#   → nothing to change on the DB side (password re-applied, harmless),
#     secrets created in Infisical. Healed without invalidating the running app.

# State C (secrets stale, database gone):
databasemanager provision billing-api --adopt
#   → db+user recreated, secrets *updated* to the new reality. Healed.
```

### 2.3 Required changes, layer by layer

#### (a) `internal/database` — adopt-aware adapters

`ProvisionOptions` gains `Adopt bool`, and `Provision` returns a report so logging
can be honest about what happened (adapters stay logger-free):

```go
type ProvisionReport struct {
    DatabaseCreated bool // false = adopted
    UserCreated     bool // false = adopted (password reset via ALTER)
}

type Database interface {
    Provision(ctx context.Context, opts ProvisionOptions) (ProvisionReport, error)
    ...
}
```

*(Interface-signature change; `MockDB` and both adapters updated together. If the
churn is unwanted, an `AdoptReport` out-field on a pointer options struct works,
but the explicit return is cleaner.)*

**Postgres (`postgres.go`):**

- New existence check: `userExists` — `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE
  rolname = $1)`, parameterized like `databaseExists` (`postgres.go:86-93`).
  (Postgres has no `CREATE USER IF NOT EXISTS`, so check-then-act it is.)
- Strict mode: behavior unchanged — exists → `database %q already exists`.
- Adopt mode:
  - user exists → `RotatePassword` (Feature 1's method — the dependency) instead
    of `CREATE USER`; else create.
  - db exists → skip `CREATE DATABASE`; optionally converge ownership with
    `ALTER DATABASE … OWNER TO …` (recommended: yes — adoption promises
    convergence; see decision table).
  - hardening re-applied; every statement is already idempotent
    (`REVOKE…FROM PUBLIC`, `ALTER SCHEMA public OWNER TO`, `GRANT ALL … TO`)
    **except** custom-schema creation, which becomes
    `CREATE SCHEMA IF NOT EXISTS x AUTHORIZATION u` followed by
    `ALTER SCHEMA x OWNER TO u` (converges a pre-existing schema's owner).
- **Rollback safety falls out of the existing design:** `provisionState` flags are
  only set when this run *created* something (`postgres.go:112-134`), so the
  deferred `rollbackProvision` inherently never drops adopted resources. This
  property must be pinned by test, not just observed.

**MySQL (`mysql.go`):**

- Adopt mode: `CREATE DATABASE IF NOT EXISTS` (pre-existing charset left alone —
  adoption is non-destructive to data); `CREATE USER IF NOT EXISTS` +
  `ALTER USER … IDENTIFIED BY …` (MySQL ≥ 5.7 — the tool already targets 8);
  `GRANT`/`FLUSH` unchanged (idempotent). Whether the db/user pre-existed is read
  from `ROW_COUNT()`/a prior existence check to fill the report; the existing
  `databaseExists` plus a symmetric `userExists`
  (`SELECT 1 FROM mysql.user WHERE user = ? AND host = ?` — admin has the
  privilege) keeps it explicit and mirrors Postgres.
- Same rollback property, same pin test.

#### (b) `internal/services` — the upsert sync

Replace the adopt-path sync with a **computed plan** (strict path can keep batch
create or reuse this — recommend reusing, one code path):

```go
type syncPlan struct {
    creates []database.SecretKV
    updates []database.SecretKV
    deletes []string // contract keys present remotely but absent from desired
}

// planSecretSync is PURE: trivially unit-testable, the heart of the feature.
// - desired ordering preserved (ToSecrets deterministic order, §6.1)
// - only CONTRACT keys are ever deleted; unknown keys in the folder are untouched
func planSecretSync(existing map[string]string, desired []database.SecretKV) syncPlan
```

Executor: `List` at `/{app}` → plan → `Update`/`Create` per key in contract order →
`Delete` stale contract keys last. Every step idempotent ⇒ a partial failure is
healed by re-running `--adopt`. The stale-delete matters: adopting a
formerly-Postgres app as MySQL must remove `DB_SCHEMA`, or every reader's
`ValidateContract` rejects the app forever (`credentials.go:116-118`).

`ProvisionResult` grows honest reporting:
`DatabaseCreated, UserCreated bool; SecretsCreated, SecretsUpdated, SecretsDeleted int` —
surfaced in the final log line
(`Successfully provisioned (adopted existing database; password reset; 6 secrets updated)`).

`prepareOptions` needs one addition: in adopt mode with existing Infisical secrets,
cross-check stored `DB_TYPE` against `--type` and **error on mismatch** (same rule
as delete) — adopting a `mysql` app with `--type postgres` must not half-convert it.

#### (c) `cmd/provision.go`

- New flags: `--adopt`, `--force` (prompt-skip; only meaningful with `--adopt`).
- Pre-flight in adopt mode: `ResolveApp` (tolerating not-found), existence probes
  for the prompt text, `confirmAction` unless `--force`.
- Phase-0 change (independent): the sync-failure error path prints
  `result.Credentials.ToSecrets()` to stdout like the offline path — in both
  strict and adopt modes.

### 2.4 Failure/recovery matrix after the feature

| Partial state | Cause | Re-run `provision --adopt` does |
|---|---|---|
| DB+user exist, no secrets (A) | sync failed | adopt both, reset password, create all secrets ✔ |
| DB+user exist, secrets exist (B) | offline provision, later Infisical available | adopt both, reset (or `--pass`) password, update secrets ✔ |
| Secrets exist, no DB (C) | manual drop / restore | recreate both, update secrets in place ✔ |
| Half a sync (some keys) | crash mid-upsert | plan recomputed from live listing; missing keys created, rest updated ✔ |
| User exists, DB doesn't | crashed strict run post-rollback-failure | create DB, adopt user ✔ |

Every row converges; every row was previously "manual psql surgery".

### 2.5 Edge cases & safety decisions

| # | Case | Decision / rationale |
|---|---|---|
| 1 | **Adopting a database this tool never created** | Allowed but *loud*: the prompt states hardening will re-own the `public` schema and revoke `PUBLIC`, and that the named user's password resets. This is the feature's sharpest edge — on Postgres, `ALTER SCHEMA public OWNER TO admin` + `REVOKE ALL … FROM PUBLIC` can break *other* users of a hand-made database. The prompt is the safety mechanism; `--force` is the informed override. Document with a warning box in README. |
| 2 | Existing DB under *different* names | Adoption targets the derived/overridden names only. A pre-existing `billing_db` is invisible to `provision billing-api --adopt` (which targets `billing_api_db`) — the run creates a parallel database rather than adopting the intended one. Not a data-loss risk; a documentation note ("pass `--db`/`--user` matching the real names"). |
| 3 | Engine switch under one app name | `DB_TYPE` mismatch between stored secrets and `--type` is an **error** (not a silent conversion). Converting an app across engines remains delete + provision. |
| 4 | Custom operator secrets in `/{app}` | Preserved — `planSecretSync` deletes only contract keys. Pinned by test. |
| 5 | Password on adopt when the app is live | Always reset (it's unknowable otherwise) — so adopt **is** a rotation, and the prompt says so. `--pass` exists precisely so State B can adopt *without* invalidating a running app. |
| 6 | `ALTER DATABASE … OWNER` on adopted pg DBs | Recommend yes (convergence promise); it's one statement and reversible. If deemed too invasive for v1, log the divergent owner instead. |
| 7 | Wrong `--env` slug | Unchanged fail-closed behavior: `ensureFolder` create fails and the List-confirm fallback doesn't find it (`provisioner.go:192-216`). |
| 8 | Strict mode default | **Completely unchanged.** `--adopt` is opt-in; every existing invariant (§11.7's validate → existence check → rollback) holds verbatim for strict runs, and adopt *refines* rather than weakens it: "rollback drops exactly what this run created" was already the state machine's contract. |

### 2.6 Test plan

| Test | What it pins |
|---|---|
| `TestPlanSecretSync_AllNew` | Empty remote → all creates, contract order |
| `TestPlanSecretSync_AllExisting` | Full remote → all updates, no creates/deletes |
| `TestPlanSecretSync_Mixed` | Partial remote (crash-mid-sync remnant) → correct split |
| `TestPlanSecretSync_DeletesStaleSchema` | mysql-desired vs remote `DB_SCHEMA` → delete planned |
| `TestPlanSecretSync_PreservesUnknownKeys` | `MY_CUSTOM_KEY` in remote → untouched |
| `TestProvisioner_Run_AdoptPlumbsFlag` | `Adopt` reaches the adapter via `ProvisionOptions` |
| `TestProvisioner_Run_ReportsAdoption` | MockDB returns `DatabaseCreated:false` → result/logs reflect adoption |
| `TestProvisioner_Run_Adopt_TypeMismatchRejected` | Stored `DB_TYPE=mysql` + `--type postgres` → error before any DB call |
| `TestProvisioner_Run_PrintsCredentialsOnSyncFailure` (Phase 0) | The password is never lost again — needs the `secretStore` fake |
| Adapter: adopt-rollback pin | A failure after adopting (not creating) must roll back nothing — with the state machine factored as it is, this is testable by driving `rollbackProvision` with adopted-state flags; full paths land in integration tests |
| `MockDB` update | New `Provision` signature + `RotatePassword` — compiler-enforced |

**Adapter reality check:** the create/adopt SQL sequences themselves still need a
real server (same limitation ARCHITECTURE §9 records for provisioning today).
Recommendation: add a `//go:build integration` test file using
`testcontainers-go` (Postgres 16 + MySQL 8) driving strict-provision →
adopt-after-secret-wipe → adopt-after-db-wipe → delete, wired to a
`make test-integration` target. This closes the README gap-table row
("integration tests") with the feature that most needs it.

### 2.7 Documentation updates

- README: provision flags table (`--adopt`, `--force`), a "Recovering from partial
  states" subsection replacing the current gap-table rows, warning box for §2.5#1.
- ARCHITECTURE: §5.1 data flow (adopt branch + upsert), §6.1 (sync becomes
  plan-based; deterministic order now also the *execution* order), §7 adapter
  table (existence checks, adopt semantics), §11 invariant 7 refined ("rollback
  drops exactly what the run created — adopted resources are never dropped"),
  §12 behavior deltas (sync-failure now prints credentials).
- HARDENING_PLAYBOOK: adopt scenarios in the live path (they double as smoke tests
  for States A–C).

### 2.8 Effort estimate & phasing

| Phase | Scope | Size |
|---|---|---|
| 0 | Print credentials on sync failure | ~10 LOC, ships immediately |
| 1 | `RotatePassword` + `rotate` command (+ optionally the `secretStore` port) | ~150–270 LOC prod + tests |
| 2 | `planSecretSync` + upsert executor | ~120 LOC prod + ~150 tests |
| 3 | `--adopt`: adapter adopt paths, report, prompt, flags, docs | ~250 LOC prod + tests |
| 4 | Integration suite (`testcontainers-go`, build-tagged) | ~200 LOC, new dev dependency |

Total: roughly 3–5 working sessions, with each phase independently shippable and
each earlier phase reducing the risk of the next.
