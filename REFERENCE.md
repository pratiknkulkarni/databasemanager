# databasemanager: full reference

Exhaustive reference for every flag, secret key, exit code and edge case.
Start with [`README.md`](README.md) for an overview and the common workflows.

---

A single-shot CLI that provisions **isolated PostgreSQL and MySQL databases** — one database,
one owning user, privilege-hardened — and records the resulting credentials as secrets in
**[Infisical](https://infisical.com) Secrets Manager**. It then reads those secrets back to
list them, emit connection strings in several formats, test connectivity, **rotate
passwords** (with automatic rollback if the secret update fails), and cleanly deprovision
everything (database, user, secrets, folder) when an app is retired. Provisioning is
**convergent on demand**: `--adopt` reconciles any partial state — orphaned databases,
missing secrets, crashed syncs — in one command.

Think of it as a tiny self-service database vending machine for a team or homelab:
`provision myapp` gives you a locked-down database whose credentials live in your secrets
manager, never in a wiki page or a Slack message.

```
$ databasemanager provision billing-api --type postgres --env dev
INFO  starting provisioning app=billing-api type=postgres
INFO  provisioning database name=billing_api_db user=billing_api_user
INFO  syncing secrets to infisical env=dev
INFO  Successfully provisioned app=billing-api db_name=billing_api_db connection="Secrets synced to Infisical"

$ databasemanager conn billing-api
┌──────────────────────┬───────────────────────────────────────────────────────────────────────────┐
│ FORMAT               │ CONNECTION STRING                                                         │
├──────────────────────┼───────────────────────────────────────────────────────────────────────────┤
│ URI                  │ postgresql://billing_api_user:*****@db.internal:5432/billing_api_db?...   │
│ PSQL Command         │ PGPASSWORD=***** psql -h db.internal -p 5432 -d billing_api_db -U ...     │
│ Environment Variable │ DATABASE_URL=postgresql://billing_api_user:*****@db.internal:5432/...     │
└──────────────────────┴───────────────────────────────────────────────────────────────────────────┘
```

**Companion documents:**

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — the as-built architecture, boundary invariants, and
  the full defect/hardening registers.
- [`HARDENING_PLAYBOOK.md`](HARDENING_PLAYBOOK.md) — hands-on walkthrough for exercising
  every security fix (unit-test fast paths + live Docker paths).

---

## Table of contents

1. [How it works](#how-it-works)
2. [Installation](#installation)
3. [Configuration](#configuration)
4. [Command reference](#command-reference)
   - [Global flags](#global-flags)
   - [`provision`](#provision)
   - [`delete`](#delete)
   - [`list`](#list)
   - [`conn`](#conn)
   - [`test`](#test)
   - [`rotate`](#rotate)
   - [Auto-generated commands](#auto-generated-commands)
5. [The secret contract](#the-secret-contract)
6. [Security model](#security-model)
7. [Output conventions & exit codes](#output-conventions--exit-codes)
8. [Test suite guide](#test-suite-guide)
9. [Missing features, nice-to-haves & known limitations](#missing-features-nice-to-haves--known-limitations)
10. [Development](#development)

---

## How it works

### Lifecycle at a glance

```
                 ┌────────────────────────────────────────────────────────────┐
                 │                       databasemanager                      │
                 └────────────────────────────────────────────────────────────┘
  provision ──►  validate names ──► CREATE USER + CREATE DATABASE ──► harden ──► sync secrets
                                                    │ (any failure)                    │
                                                    ▼                                  ▼
                                          rollback (drop what                 Infisical  /{app}
                                          was created, 10s budget)            DB_TYPE, DB_NAME,
                                                                              DB_USER, DB_PASSWORD,
  conn/test/list ──► read /{app} secrets ◄────────────────────────────────── DB_HOST, DB_PORT,
                          │                                                   DB_SCHEMA (pg only)
                          ▼
              connection strings / ping / tables

  delete ──► resolve recorded DB_NAME/DB_USER from Infisical ──► confirm ──► DROP DATABASE +
             (never guessed from the app name)                               DROP USER ──► delete
                                                                              secrets ──► delete folder

  rotate ──► resolve recorded DB_USER ──► confirm ──► ALTER USER (new password) ──► update
             DB_PASSWORD in Infisical (on failure: restore old password) ──► verify dial

  provision --adopt ──► converge: keep existing db/user (reset password), re-apply
                        hardening, reconcile secrets — heals any partial state
```

### Process model

- **One process per invocation, single goroutine.** `main.go` installs a
  `signal.NotifyContext(SIGINT, SIGTERM)` context that flows through every command — Ctrl-C
  cancels in-flight SQL. Provisioning rollback deliberately runs on a *fresh* 10-second
  context so cleanup still completes after you cancel.
- **Two-stage composition root.** `main.go` builds the logger and an empty dependency
  container; `cmd/root.go`'s `PersistentPreRunE` (after Cobra parses flags, so `--config`
  and `--verbose` actually work) loads configuration and authenticates the Infisical client.
- **Infisical failure is warn-and-continue.** If Infisical is unreachable at startup you get
  a warning, not a crash. Each command then decides: `list`/`conn`/`test`/`delete` hard-fail
  (they need secrets), while `provision` proceeds **offline** and prints the generated
  credentials to stdout — the only copy, so record them.
- **Engine adapters are per-invocation.** The command that needs a database constructs the
  adapter via a registry (`database.New`) and closes it on exit. Connections are dialed
  lazily.

### Package map

| Package | Responsibility |
|---|---|
| `main` | Signal context, logger, container shell |
| `cmd` | Cobra commands: flags, prompts, rendering (tables/JSON/masking), connection-string generation |
| `internal/app` | Immutable dependency container (`Config`, `Logger`, `Infisical`) |
| `internal/services` | `Provisioner`: provision/deprovision lifecycle + password generation |
| `internal/database` | Engine registry, Postgres/MySQL adapters, `Credentials` secret contract, validation |
| `internal/infisical` | Infisical client construction (Universal Auth) |
| `internal/config` | Viper config loading + validation (file + env) |
| `internal/logger` | Styled charmbracelet logger on stderr |

---

## Installation

Requires **Go 1.25+**.

```bash
git clone https://github.com/praaatik/databasemanager
cd databasemanager

make build            # → ./databasemanager
# or
go build -o dbm .     # any binary name you like
# or
go install github.com/praaatik/databasemanager@latest
```

Verify:

```bash
./databasemanager --help
make test             # run the unit test suite (no database or Infisical needed)
```

---

## Configuration

Configuration comes from a **YAML file**, **environment variables**, or both
(environment wins).

### File location

| Priority | Location |
|---|---|
| 1 | Explicit path: `--config /path/to/file.yaml` |
| 2 | `$HOME/.databasemanager.yaml` |
| 3 | `./.databasemanager.yaml` (current directory) |

> The repository's `.gitignore` deliberately excludes `.databasemanager.yaml*` (including
> backup suffixes) — this file holds live admin credentials and must never be committed.

### Full example

```yaml
# --- Infisical (required for all commands) ---
infisical_project_id: "your-project-id"
infisical_site_url: "https://app.infisical.com"   # optional; defaults to Infisical Cloud

# Universal Auth (recommended): re-authenticates every run, nothing expires.
infisical_client_id: "machine-identity-client-id"
infisical_client_secret: "machine-identity-client-secret"

# Token Auth (alternative): a pre-issued access token, used as-is. Supply this
# *instead of* the pair above; it wins if both are set. Note the token dies
# permanently at its max TTL (30 days default) and must be reissued by hand.
# infisical_access_token: "eyJhbGciOi…"

# --- Engine sections (each optional as a whole; required fields if present) ---
postgres:
  database_hostname: "db.internal"
  database_port: 5432
  database_user: "postgres"          # admin/superuser used to provision
  database_password: "admin-password"
  database_name: "postgres"          # maintenance DB the admin dial connects to
  # database_sslmode: "require"      # optional; omit for the secure default

mysql:
  database_hostname: "db.internal"
  database_port: 3306
  database_user: "root"
  database_password: "admin-password"
  database_name: "mysql"
  # database_sslmode: "skip-verify"  # optional; omit for the secure default
  # database_user_host: "10.0.%"     # optional; scopes provisioned accounts (default '%')
```

### Key reference

| Key | Required | Default | Meaning |
|---|---|---|---|
| `infisical_project_id` | **yes** | — | Infisical project that stores app secrets |
| `infisical_client_id` | one of† | — | Universal Auth machine-identity client ID |
| `infisical_client_secret` | one of† | — | Universal Auth machine-identity client secret. Must be the UA secret, **not** an access token — a JWT here is rejected at startup with a pointer to `infisical_access_token` |
| `infisical_access_token` | one of† | — | Token Auth pre-issued access token, used directly as a bearer token. Takes precedence over the UA pair |
| `infisical_site_url` | no | Infisical Cloud | Self-hosted Infisical URL |
| `<engine>.database_hostname` | if section present | — | DB server host the admin connection dials; also recorded as `DB_HOST` for apps |
| `<engine>.database_port` | if section present | — | DB server port; also recorded as `DB_PORT` |
| `<engine>.database_user` | if section present | — | Admin account with `CREATEDB`/`CREATEROLE` (pg) or `CREATE USER`/`GRANT` (mysql) rights |
| `<engine>.database_password` | if section present | — | Admin password |
| `<engine>.database_name` | if section present | — | Database the admin connection attaches to (`postgres` / `mysql` typically) |
| `<engine>.database_sslmode` | no | `require` (pg) / `skip-verify` (mysql) | Transport encryption. Postgres: libpq values `disable\|require\|verify-ca\|verify-full`. MySQL: same vocabulary mapped onto the driver (`disable/false`→off, `require/skip-verify`→encrypted without cert verification, `verify-ca/verify-full/true`→full verification). Setting `disable` logs a loud warning on every dial |
| `mysql.database_user_host` | no | `%` | Host part of the provisioned account (`'user'@'<host>'`). Scope it (e.g. `10.0.%`) so app accounts aren't reachable network-wide. **Keep it stable** — `delete` drops `'user'@'<configured host>'`, so changing it later orphans accounts created under the old value |

† Supply **either** the `infisical_client_id` + `infisical_client_secret` pair (Universal Auth)
**or** `infisical_access_token` (Token Auth). Whichever you choose, the machine identity must
also be **added to the project** under *Project → Access Control → Machine Identities*.
Org-level membership is not enough: authentication succeeds but every secret call returns
`403 ProjectMembershipNotFound`.

A config with **no engine section at all is valid** — `list`, `conn`, and `test` only need
Infisical. Commands that need an engine get a clear error
(`postgres is not configured: set the postgres section…`) at the moment they need it.

### Environment variables

Every key is bindable with the `DATABASEMANAGER_` prefix; dots become underscores. The tool
runs fine with **no config file at all**:

```bash
export DATABASEMANAGER_INFISICAL_PROJECT_ID="…"
export DATABASEMANAGER_INFISICAL_CLIENT_ID="…"
export DATABASEMANAGER_INFISICAL_CLIENT_SECRET="…"
# …or, for Token Auth instead of the pair above:
# export DATABASEMANAGER_INFISICAL_ACCESS_TOKEN="eyJhbGciOi…"
export DATABASEMANAGER_POSTGRES_DATABASE_HOSTNAME="db.internal"
export DATABASEMANAGER_POSTGRES_DATABASE_PORT="5432"
export DATABASEMANAGER_POSTGRES_DATABASE_USER="postgres"
export DATABASEMANAGER_POSTGRES_DATABASE_PASSWORD="…"
export DATABASEMANAGER_POSTGRES_DATABASE_NAME="postgres"
```

---

## Command reference

```
databasemanager [command] [args] [flags]

Commands:
  provision   Provision a new database and sync secrets
  delete      Deprovision a database and remove its secrets from Infisical
  list        List secrets stored in Infisical
  conn        Get database connection string for an app
  test        Test connection using credentials stored in Infisical
  rotate      Rotate an app's database password and update Infisical
  completion  Generate shell autocompletion (auto-generated by Cobra)
  help        Help about any command
```

### Global flags

Available on every command.

| Flag | Type | Default | Detail |
|---|---|---|---|
| `--config <path>` | string | *(search, see above)* | Absolute or relative path to a specific config file. When set, **only** that file is read — the `$HOME`/`.` search is skipped. A missing or unparsable file is a hard error. |
| `--verbose`, `-v` | bool | `false` | Raises the logger from `INFO` to `DEBUG`. Debug output includes things like "infisical folder already exists". All log output goes to **stderr**, so `-v` never corrupts piped stdout data. |

---

### `provision`

```
databasemanager provision <app-name> [flags]
```

Creates a database + owning user on the target engine, applies privilege hardening
(Postgres), and writes the credentials to Infisical under folder `/<app-name>` in the chosen
environment.

**What actually happens, in order:**

1. Names are derived from `<app-name>` (hyphens → underscores) unless overridden, then
   validated against a strict identifier allowlist (`^[A-Za-z_][A-Za-z0-9_]*$`, DB name
   ≤ 63 chars, user ≤ 32 chars).
2. A 32-hex-character password (16 bytes of `crypto/rand`, 128 bits of entropy) is generated
   unless `--pass` is given. If the OS entropy pool is unreadable, provisioning **fails
   hard** — there is no weak fallback.
3. **The secret store is pre-flighted before the database is touched:** the app folder is
   created (fail-closed), and the app must not already have recorded contract secrets. A
   wrong `--env` slug, an Infisical outage, or an already-recorded app fails *here*, while
   there is still nothing to clean up.
4. The engine checks the database doesn't already exist (parameterized catalog query);
   an existing database is a hard error, nothing is touched.
5. **Postgres:** `CREATE USER … WITH PASSWORD …` → `CREATE DATABASE … OWNER …`, then a
   hardening pass *inside the new database*: `REVOKE CONNECT … FROM PUBLIC`, re-own the
   `public` schema to the admin, `REVOKE ALL ON SCHEMA public FROM PUBLIC`, then either
   grant the app user `ALL` on `public` or `CREATE SCHEMA <custom> AUTHORIZATION <user>`.
   **MySQL:** `CREATE DATABASE … utf8mb4/utf8mb4_unicode_ci` → `CREATE USER 'u'@'<host>'` →
   `GRANT ALL PRIVILEGES ON db.* TO …` → `FLUSH PRIVILEGES`.
6. **Any failure rolls back exactly what was created** (state machine drops the DB and/or
   user), on a fresh 10-second context so cleanup survives Ctrl-C.
7. Credentials are written to Infisical as a **computed reconciliation plan** — per-key
   creates in the contract's deterministic order, so a partial failure names exactly the
   key that did not land. If the Infisical client is unavailable, the run completes with a
   warning and prints the `KEY=VALUE` credentials to **stdout** — *the only copy of the
   generated password; capture it.* If the sync fails after the database was created, the
   error names the orphaned database **and the same credential dump is printed** so the
   generated password is never lost.

**Flags:**

| Flag | Type | Default | Detail |
|---|---|---|---|
| `--type` | string | `postgres` | Target engine: `postgres` or `mysql`. Anything else fails fast with the supported list. The engine's config section must exist. Recorded in the `DB_TYPE` secret so later commands never need to be told again. |
| `--env` | string | `dev` | Infisical **environment slug** the secrets are written to (`dev`, `staging`, `prod`, …). Must exist in the Infisical project. This does *not* select a different database server — server targeting comes from the engine config section. |
| `--user` | string | *(derived)* | Override the database user name. Default: `<app>_user` with hyphens sanitized to underscores (`billing-api` → `billing_api_user`). Must match the identifier allowlist, ≤ 32 chars (MySQL's limit; the lower bound of both engines). |
| `--pass` | string | *(generated)* | Override the password. Default: 32-hex crypto-random. Any characters are safe — every downstream path (DSNs, URIs, shell commands) quotes/encodes correctly — but prefer the generated one. |
| `--db` | string | *(derived)* | Override the database name. Default: `<app>_db` sanitized. Identifier allowlist, ≤ 63 chars (Postgres's limit). |
| `--schema` | string | `public` (pg) | **Postgres only.** Schema for the app. `public` grants the app user `ALL` on the (locked-down) public schema; any other value creates that schema `AUTHORIZATION <app user>`. Passing `--schema` with `--type mysql` is **rejected up front** — it would record a `DB_SCHEMA` secret every reader rejects for MySQL. |
| `--host` | string | *(engine config)* | Override the host **recorded** in the `DB_HOST` secret. Useful when apps reach the server via a different address than the admin (e.g. you dial `127.0.0.1`, apps use `db.svc.cluster.local`). Does *not* change where provisioning connects. |
| `--port` | int | *(engine config)* | Override the **recorded** `DB_PORT`, same semantics as `--host`. |
| `--adopt` | bool | `false` | **Convergent provisioning.** Pre-existing database/user are kept instead of causing an error, and recorded secrets are reconciled in place. See [Adoption & recovery](#adoption--recovery) below — this is the one-command healer for every partial-failure state. Implies a password reset for the app user. |
| `--force` | bool | `false` | Skip the adoption confirmation prompt. Only meaningful together with `--adopt` (strict provisioning never prompts). |

**Examples:**

```bash
# Simplest: postgres, dev, everything derived
databasemanager provision billing-api

# MySQL for production, custom database name
databasemanager provision billing-api --type mysql --env prod --db billing

# Postgres with a dedicated schema and the app-facing hostname recorded
databasemanager provision analytics --schema analytics --host db.prod.internal
```

**Failure modes worth knowing:** `database "x" already exists (re-run with --adopt to
converge it)` (nothing changed); `app "x" already has recorded secrets … re-run with
--adopt to reconcile` (nothing changed — caught in pre-flight); `invalid provision
options: database user "…" exceeds 32 characters`; TLS handshake failure against a
non-TLS server (see [Security model](#security-model) — set `database_sslmode: disable`
explicitly for plaintext local dev).

#### Adoption & recovery

`--adopt` switches provisioning from *create-or-die* to **converge-to-desired-state**:

| Resource | Absent | Present |
|---|---|---|
| Database | created (as normal) | **adopted** — data preserved; ownership converged onto the app user (Postgres `ALTER DATABASE … OWNER TO`) |
| User | created (as normal) | **adopted** — kept, but its password is **reset** to the run's known one (the old password is unrecoverable: servers store only hashes) |
| Hardening / grants | applied | **re-applied idempotently** (custom schemas use `CREATE SCHEMA IF NOT EXISTS` + owner convergence) |
| Secrets | created | **reconciled**: stale values updated, missing keys created, stale *contract* keys deleted (e.g. a leftover `DB_SCHEMA`) — operator-owned extras in the folder are never touched |

Safety rails:

- **Confirmation prompt** (skip with `--force`) states exactly what adoption does —
  password reset, hardening re-application, secret reconciliation — because it mutates
  resources this run did not create. ⚠️ *Adopting a hand-made database applies the same
  lockdown as a fresh one (on Postgres, `PUBLIC` loses access and the `public` schema is
  re-owned): fine for databases this tool created, potentially disruptive for others.*
- **Engine identity is checked before any mutation**: adopting an app recorded as `mysql`
  with `--type postgres` is refused. Adoption converges an app; it never converts one
  across engines. (A *legacy* app with no `DB_TYPE` adopts cleanly and gains the full
  contract — adoption doubles as the legacy-upgrade path.)
- **Rollback still only drops what the run created.** Adopted resources are never dropped
  by a failed run's cleanup.
- Names are matched by the same derivation/override rules as normal provisioning — to
  adopt a database provisioned under custom names, pass the same `--db`/`--user`.

**Every partial state has the same one-command recovery:**

```bash
# State A — database created, secret sync failed (password was printed at failure time):
databasemanager provision billing-api --adopt
#   → db+user adopted, fresh password applied, all secrets created. Healed.

# State B — offline provisioning (Infisical was down; credentials captured from stdout):
databasemanager provision billing-api --adopt --pass '<captured-password>'
#   → db side re-converged with the SAME password (no app disruption), secrets recorded.

# State C — secrets exist but the database is gone (dropped by hand / dev reset):
databasemanager provision billing-api --adopt
#   → db+user recreated, recorded secrets updated to the new reality. Healed.

# Crash mid-sync — some keys landed, some didn't:
databasemanager provision billing-api --adopt
#   → plan recomputed from what actually exists; only the gaps are written.
```

---

### `delete`

```
databasemanager delete <app-name> [flags]
```

Deprovisions an app: drops its database and user, then removes its secrets and folder from
Infisical.

**Resolution is from Infisical, never by naming convention.** The command reads the app's
recorded `DB_NAME`/`DB_USER`/`DB_TYPE` secrets — so it deletes exactly what was provisioned,
even if it was provisioned with `--db`/`--user` overrides. Those names are then re-validated
against the identifier allowlist *before any SQL is built or any connection dialed*, so a
tampered Infisical secret cannot inject SQL into the admin connection.

**Order matters:** database drop first; then each secret; then the folder. If the DB drop
fails, the secrets remain (the app stays resolvable for a retry). If a post-drop step fails,
the error is prefixed `database deleted, but failed to …` so partial state is never silent.

Postgres additionally force-terminates active backends on the database (best-effort
`pg_terminate_backend`) before dropping, since Postgres refuses to drop a database with live
connections.

**Flags:**

| Flag | Type | Default | Detail |
|---|---|---|---|
| `--type` | string | *(empty)* | **Legacy fallback only.** Modern apps carry `DB_TYPE`, which is authoritative. If `DB_TYPE` exists and `--type` disagrees, that's an error (protects against dropping on the wrong engine). Only apps provisioned before `DB_TYPE` existed need `--type` to state their engine. |
| `--env` | string | `dev` | Infisical environment to resolve (and delete) the app from. |
| `--force` | bool | `false` | Skip the interactive confirmation. Without it, the command prints exactly what will be deleted (database, user, **host**, app, environment) to **stderr** and requires `y`/`yes` on stdin. Anything else aborts safely. |

**Examples:**

```bash
databasemanager delete billing-api --env dev
# About to delete database "billing_api_db" and user "billing_api_user"
# for app "billing-api" (environment "dev").
# Type 'y' or 'yes' to confirm: y

databasemanager delete billing-api --env prod --force     # CI / scripted
databasemanager delete ancient-app --type mysql           # pre-DB_TYPE legacy app
```

---

### `list`

```
databasemanager list [APP] [SECRET_KEY] [flags]
```

Read-only view over Infisical. Arity selects the scope:

| Args | Behavior |
|---|---|
| *(none)* | List all apps (Infisical folders) in the environment |
| `APP` | List all secrets for that app |
| `APP SECRET_KEY` | Show a single secret |

**Masking is on by default** and two-layered:

1. **By key:** any key containing `PASSWORD`, `PASSWD`, `PWD`, `KEY`, `SECRET`, `TOKEN`, or
   `CREDENTIAL` (case-insensitive) renders as `*****`.
2. **By value:** even under an innocuous key (`DB_URI`, `DSN`), a value shaped like
   `scheme://user:password@host` gets just its password portion masked
   (`postgres://u:*****@host:5432/db`) — an embedded password never prints in the clear.

**Flags:**

| Flag | Type | Default | Detail |
|---|---|---|---|
| `--env` | string | `dev` | Infisical environment to read. |
| `--show-values` | bool | `false` | Disable masking and print real values. This is a display toggle, not access control — anyone able to run the CLI holds the Infisical machine identity anyway. Combine with `--format json` to feed other tooling. |
| `--format` | string | `table` | `table` (pretty-printed) or `json` (machine-readable, 2-space indented). Because *all* status chrome goes to stderr, `--format json` pipes cleanly: `databasemanager list myapp --format json \| jq …`. Empty results render as `[]` in JSON mode. |

**Examples:**

```bash
databasemanager list                                    # all apps in dev
databasemanager list --env prod                         # all apps in prod
databasemanager list billing-api                        # secrets, masked
databasemanager list billing-api --show-values          # secrets, real values
databasemanager list billing-api DB_PASSWORD --show-values --format json \
  | jq -r '.secret_value'                               # extract one value
```

---

### `conn`

```
databasemanager conn <app> [flags]
```

Renders ready-to-use connection strings from the app's stored secrets. The engine comes from
the recorded `DB_TYPE`; the full secret contract is validated first (core fields present,
schema present iff Postgres).

**Formats by engine:**

| Format | Postgres | MySQL | Output |
|---|---|---|---|
| `table` (default) | ✔ | ✔ | All formats at once, **password always masked** (`*****`) |
| `uri` | ✔ | ✔ | `postgresql://user:pass@host:port/db?search_path=schema` / `mysql://user:pass@host:port/db` — credentials percent-encoded, so any password round-trips |
| `psql` | ✔ | ✘ | `PGPASSWORD=… psql -h … -p … -d … -U …` — every value shell-quoted, safe to paste |
| `mysql` | ✘ | ✔ | `mysql -h … -P … -D … -u … -p…` — shell-quoted likewise |
| `env` | ✔ | ✔ | `DATABASE_URL=<uri>` |

Requesting a format foreign to the engine (`psql` for a MySQL app) is a descriptive error.

The table's masking works **by regeneration**: the string is rebuilt with a `*****` marker
password rather than pattern-matched afterwards, so the real password cannot leak through
the mask no matter what characters it contains.

**Flags:**

| Flag | Type | Default | Detail |
|---|---|---|---|
| `--env` | string | `dev` | Infisical environment to read the app from. |
| `--format` | string | `table` | One of the formats above. `table` is human-oriented and always masked; the other formats print the **real** connection string to stdout (for piping into `.env` files etc.). |
| `--type` | string | *(empty)* | Cross-check, not an override: if provided and it disagrees with the stored `DB_TYPE`, the command errors. It cannot substitute for a missing `DB_TYPE` (legacy apps must be re-provisioned for `conn`/`test`; only `delete` has a legacy fallback). |
| `--copy` | bool | `false` | Copy the connection string to the clipboard instead of printing it. **Requires an explicit non-table `--format`.** On success it logs a confirmation with a middle-masked preview (`su***23`) so the real password stays off the terminal. If no clipboard is available (headless SSH), it falls back to printing the *unmasked* string on stdout with a warning. |

**Examples:**

```bash
databasemanager conn billing-api                          # masked overview table
databasemanager conn billing-api --format uri             # real URI on stdout
databasemanager conn billing-api --format env >> .env     # append DATABASE_URL
databasemanager conn billing-api --format psql --copy     # psql one-liner → clipboard
eval "$(databasemanager conn billing-api --format psql)"  # connect right now
```

---

### `test`

```
databasemanager test <app-name> [flags]
```

Dials the database **as the app** — with the provisioned credentials from Infisical, not the
admin account — and pings it. Proves, end to end: the secrets are intact, the server is
reachable, the user exists with that password, and the database accepts the connection under
the engine's TLS posture (from the engine's config section; secure default when unset).

MySQL dials carry a 5-second timeout; Postgres relies on the command context (Ctrl-C
cancels).

| Flag | Type | Default | Detail |
|---|---|---|---|
| `--env` | string | `dev` | Infisical environment to read the app's credentials from. |

```bash
databasemanager test billing-api --env prod
# INFO testing connection app=billing-api type=postgres env=prod
# INFO connection successful!
```

Exit code 1 with `connection test failed: …` on any failure — usable directly as a health
gate in scripts/CI.

---

### `rotate`

```
databasemanager rotate <app-name> [flags]
```

Issues a new password for the app's database user, applies it to the database, and updates
the `DB_PASSWORD` secret in Infisical — completing the credential lifecycle without any
manual `ALTER USER` + secrets-UI editing.

**Consistency model — the part worth understanding.** Two stores must change together (the
database's password hash and Infisical's `DB_PASSWORD`) and no transaction spans them, so
rotation is engineered to fail safe:

1. The recorded user name is resolved from Infisical (the source of truth, same as
   `delete`) and re-validated against the identifier allowlist before any SQL.
2. The **database changes first** (`ALTER USER … WITH PASSWORD` / `ALTER USER …
   IDENTIFIED BY`, fully quoted/escaped).
3. `DB_PASSWORD` is updated in Infisical. If the secret was manually deleted (the
   lost-password case), it is **created** instead — rotation doubles as password recovery.
4. If step 3 fails, the database is **rolled back to the old password** (on a fresh
   10-second context, so it survives Ctrl-C): a failed rotation changes *nothing*.
5. If even the rollback fails, the new password is printed to **stdout** as the operator's
   only copy, and the error names the exact partial state. Re-running `rotate` always
   converges — there is no stuck state.
6. Unless `--no-verify`: the app credentials are dialed end-to-end
   (`database.TestAppConnection`) to prove the rotation actually works.

**What rotation does *not* do:** kill live sessions. Both engines check passwords at
connection time only, so running apps keep their connections until they reconnect — at
which point they must have re-read the secret.

**Flags:**

| Flag | Type | Default | Detail |
|---|---|---|---|
| `--env` | string | `dev` | Infisical environment the app lives in. |
| `--pass` | string | *(generated)* | Override the new password. Default: 32-hex `crypto/rand` (128-bit), same generator as provision. Must be non-empty; any characters are safe downstream. |
| `--type` | string | *(empty)* | Legacy fallback for apps missing `DB_TYPE`, identical semantics to `delete --type`: authoritative stored type wins, disagreement is an error. |
| `--force` | bool | `false` | Skip the confirmation prompt. The prompt names the user, app, database, **host**, and environment — read it; wrong-cluster rotation is as damaging as wrong-cluster deletion. |
| `--no-verify` | bool | `false` | Skip the post-rotation connectivity check. Use when the recorded `DB_HOST` (an app-facing address recorded via `provision --host`) is not reachable from your machine — the check dials the *recorded* host, not the admin host. |

**Examples:**

```bash
databasemanager rotate billing-api                      # prompt, rotate, verify
databasemanager rotate billing-api --env prod --force   # scripted rotation
databasemanager rotate billing-api --pass 'S3cure!New'  # bring your own password
databasemanager rotate ancient-app --type mysql         # legacy app without DB_TYPE
databasemanager rotate billing-api --no-verify          # recorded host unreachable from here
```

**Failure modes worth knowing:** `infisical update failed (database password restored,
nothing changed)` — safe to re-run later; `rotation succeeded but verification failed` —
the rotation *is* applied, investigate reachability; a MySQL error hinting at
`database_user_host` means the configured user-host no longer matches the one used at
provision time (the account is `'user'@'<host>'`).

---

### Auto-generated commands

Cobra provides two commands for free:

- `databasemanager completion bash|zsh|fish|powershell` — shell autocompletion scripts.
- `databasemanager help [command]` — detailed help.

---

## The secret contract

Every provisioned app owns one Infisical folder `/<app-name>` per environment, holding:

| Key | Example | Notes |
|---|---|---|
| `DB_TYPE` | `postgres` | Authoritative engine; `conn`/`test`/`delete` resolve it |
| `DB_NAME` | `billing_api_db` | The actual database name (post-override, post-sanitize) |
| `DB_USER` | `billing_api_user` | The owning user |
| `DB_PASSWORD` | `4f2b…` (32 hex) | The app's password — the one mutable key (`rotate` updates it in place) |
| `DB_HOST` | `db.internal` | Sourced from the engine config (or `--host`) — matches what was actually dialed |
| `DB_PORT` | `5432` | Same sourcing as host |
| `DB_SCHEMA` | `public` | **Postgres only**; omitted entirely for MySQL |

The contract has a single owner in code (`internal/database/credentials.go`): the
provisioner serializes via `ToSecrets()` (deterministic order), every reader parses via
`ParseCredentials()`, and validation (`ResolveType`, `ValidateContract`) produces the
user-facing diagnostics ("re-provision this app", "DB_SCHEMA found but DB_TYPE is
'mysql'…"). Renaming a key is one edit with compiler support.

---

## Security model

The threat model assumes the CLI operator already holds the DB-admin and Infisical
credentials; the defended boundaries are the **network**, the **rendered output**, and
**Infisical-as-input** (a tampered secret must not become SQL). Highlights — full details
and the audit trail live in `ARCHITECTURE.md` §13:

- **TLS by default, opt-out is loud.** Admin and app dials require encryption
  (`sslmode=require` for Postgres, `tls=skip-verify` for MySQL) unless the engine section
  explicitly sets `database_sslmode: disable` — which logs
  `TLS is DISABLED for this connection — credentials travel in cleartext` on **every** dial.
- **Identifier allowlist everywhere.** `^[A-Za-z_][A-Za-z0-9_]*$` with engine length caps,
  enforced on the provision path *and re-applied on delete* to the names read back from
  Infisical, before any SQL or dial.
- **No raw interpolation.** Postgres uses `pq.QuoteIdentifier`/`pq.QuoteLiteral` and every
  admin-DSN value is libpq-quoted (a password of `x host=evil` cannot redirect the dial).
  MySQL identifiers are backtick-doubled and literals escaped **SQL-mode-aware** — the
  client reads `@@session.sql_mode` at connect and doubles quotes under
  `NO_BACKSLASH_ESCAPES` instead of backslash-escaping.
- **Encoded URIs, shell-safe commands.** Connection URIs carry credentials through
  `url.UserPassword` (percent-encoded; `p@ss/w?rd` can't smuggle a host), and the
  `psql`/`mysql` command formats shell-quote every value (`a;rm -rf /` stays inert).
- **Masking by construction.** `conn` tables regenerate with a marker password;
  `list` masks by key keywords *and* by `user:pass@` URI shape in values.
- **Hard-fail entropy.** Password generation refuses to run if `crypto/rand` fails; there is
  no `math/rand` fallback.
- **Scoped MySQL accounts.** `database_user_host` narrows `'user'@'%'` to your network.
- **Least-privilege Postgres databases.** `PUBLIC` loses `CONNECT` on the new database and
  all rights on its `public` schema; only the app user is granted back.
- **Fail-safe mutations across two stores.** Rotation restores the old database password
  when the secret update fails; provisioning pre-flights the secret store before touching
  the database; and whenever a generated password would otherwise be lost, it is printed
  to the terminal as the operator's only copy — never silently dropped.

---

## Output conventions & exit codes

- **stdout is data, stderr is chrome.** Tables, JSON, connection strings, and the offline
  credential dump go to stdout; every log line (including `--verbose` debug output and the
  delete confirmation prompt) goes to stderr. `--format json | jq` always works.
- **Exit codes:** `0` success, `1` any error (Cobra usage errors included; usage text is
  suppressed on runtime errors via `SilenceUsage`).
- Logs are human-styled (colored level badges, timestamps, caller info) via
  `charmbracelet/log`.

---

## Test suite guide

`make test` / `go test ./...` — **58 top-level tests, 118 including subtests**, all pure unit
tests: no database server or Infisical needed, and the suite passes under `-race`. What each
one proves:

### `cmd` — connection strings & masking (`conn_test.go`)

| Test | What it proves |
|---|---|
| `TestGeneratePostgres` (6 subtests) | Postgres formats render exactly: `uri` with/without `?search_path=<schema>`, `psql` command shape, `env` (`DATABASE_URL=…`) with/without schema; the `mysql` format is rejected for a Postgres app |
| `TestGenerateMySQL` (4 subtests) | MySQL formats render exactly: `uri`, `mysql` CLI command, `env`; the `psql` format is rejected for a MySQL app |
| `TestGenerateDispatch` | `Generate()` routes by `DBType` and errors on an unknown engine (`oracle`) instead of guessing |
| `TestGenerateAll` | The "all formats" path returns exactly 3 formats per engine and propagates errors (an unknown type is an error, not an empty table) |
| `TestGenerate_SpecialCharPasswordIsEncoded` | **Security pin (S4):** a password `p@ss/w?rd&x` is percent-encoded in the URI — the first `@` cannot terminate the userinfo early and corrupt/redirect host and database |
| `TestShellQuote` (7 cases) | Shell quoting: safe strings pass through; empty → `''`; spaces, `;`, `$(whoami)`, and embedded single quotes are quoted/escaped so they can't split arguments or execute |
| `TestGeneratePSQL_ShellSafe` | **Security pin (S6):** a password of `a;rm -rf /` renders as `PGPASSWORD='a;rm -rf /' psql …` — inert when pasted |
| `TestMaskByRegeneration` | **Security pin (S6):** table masking rebuilds strings with a `*****` marker; a password `p@ss word` leaks in *no* format, in no encoding (`%20` checked too) |
| `TestMaskPasswordString` | Clipboard preview: short passwords fully masked (`***`), long ones keep 2+2 edge chars (`su***23`) |
| `TestMaskSecretValue` (12 cases) | **Security pin (S7):** `list` masking — sensitive keywords (`PASSWORD`, `PWD`, `KEY`, `SECRET`, `TOKEN`, `CREDENTIAL`, case-insensitive) render `*****`; non-sensitive keys pass through; a `postgres://u:s3cr3t@host/db` value under an innocuous key (`DB_URI`, `DSN`) has just its password masked |
| `TestResolveEngine` (5 subtests) | The shared engine-resolution policy (`delete`/`rotate`): stored `DB_TYPE` is authoritative, `--type` is only a legacy fallback, missing both is an actionable error, disagreement is rejected — never silently overridden |

### `internal/config` (`config_test.go`)

| Test | What it proves |
|---|---|
| `TestLoad_EnvOnly` | The tool is fully configurable from `DATABASEMANAGER_*` env vars with **no config file** — every key is explicitly `BindEnv`-ed (including nested `postgres.database_port`, parsed to int) |
| `TestConfig_Validate` (3 subtests) | A Postgres-only config validates; missing Infisical fields fail; an engine section with only a hostname fails as incomplete |

### `internal/database` — contract, validation, engines

| Test | What it proves |
|---|---|
| `TestCredentials_RoundTrip` (2) | `ToSecrets()` → `ParseCredentials()` is lossless for both a Postgres app (with schema) and MySQL (without) |
| `TestCredentials_ToSecrets_OmitsEmptySchema` | `DB_SCHEMA` is never written for schema-less (MySQL) apps |
| `TestCredentials_ToSecrets_DeterministicOrder` | Secret serialization order is stable (`DB_TYPE`, `DB_NAME`, `DB_USER`, `DB_PASSWORD`, `DB_HOST`, `DB_PORT`, `DB_SCHEMA`) — diffable Infisical writes |
| `TestCredentials_ResolveType` (4) | `DB_TYPE` resolution: `postgres`/`mysql` accepted; missing → "re-provision" guidance; `oracle` → invalid-value error |
| `TestCredentials_ValidateContract` (5) | Full-contract validation: Postgres requires a schema, MySQL must not have one, and missing core fields are reported **by name** (`DB_PORT, DB_USER, DB_PASSWORD`) |
| `TestProvisionOptions_Validate` (11) | The identifier gate: valid names (with/without schema) pass; rejects empty names, hyphens, leading digits, SQL metacharacters (`db"; DROP TABLE x;--`), > 63-char DB names, empty/> 32-char users, bad schemas, empty passwords |
| `TestTestAppConnection_RejectsBadInput` (2) | The app-dial entry point rejects unsupported engines (with the sorted supported list) and missing core secrets before ever dialing |
| `TestEngines_Sorted` | The engine registry lists `mysql, postgres` sorted — stable error messages |
| `TestNew` (3) | The factory rejects unknown engines, rejects a known-but-unconfigured engine with actionable text, and constructs adapters for every registered engine |
| `TestEscapeMySQLLiteral` (6) | Default-mode escaping: quotes and backslashes escaped, including the trailing-backslash case that would otherwise escape the closing quote |
| `TestMySQLClient_escapeLiteral` | **Security pin (S2):** escaping follows the server's SQL mode — under `NO_BACKSLASH_ESCAPES` quotes are doubled and backslashes left alone; otherwise backslash-escaped |
| `TestMySQLTLSParam` (6 cases) | **Security pin (S1):** sslmode→driver-tls mapping: empty → `skip-verify` (secure default), `disable/false` → off, `require` → `skip-verify`, `verify-full` → `true`, unknown values pass through as custom config names |
| `TestMySQLDelete_RejectsBadIdentifiers` | **Security pin (S2):** `Delete` on a client with **no connection** rejects `db'; DROP DATABASE prod; --` — a malicious Infisical secret is refused before any dial |
| `TestMySQLRotate_RejectsBadInput` | The rotate path applies the same pre-dial gates: Infisical-sourced user names pass the identifier allowlist, empty passwords are refused before any SQL |
| `TestPostgresDSN_QuotesValuesAndDefaultsSecure` | **Security pin (S3):** an admin password of `x host=evil.example` is quoted into the DSN instead of injecting a second `host=` keyword; `sslmode=require` is the unset default |
| `TestPostgresDSN_HonoursConfiguredSSLMode` | An explicit `database_sslmode: disable` reaches the DSN (the opt-out works) |
| `TestPostgresDelete_RejectsBadIdentifiers` | **Security pin (S2):** the same pre-dial identifier gate on the Postgres delete path (`u"; DROP ROLE x; --` refused) |
| `TestPostgresRotate_RejectsBadInput` | Postgres mirror of the rotate-path gates (bad identifier, empty password — both refused pre-dial) |

### `internal/services` — provisioning lifecycle

| Test | What it proves |
|---|---|
| `TestGenerateRandomPassword` (4) | Lengths 0/4/16/64 bytes produce hex of exactly 2× length, valid hex, no error |
| `TestProvisioner_Run_Defaults` | The default derivation chain end-to-end (via a mock DB): `test-service` → `test_service_db` + `test_service_user` (hyphens sanitized in **both**), schema defaults to `public`, a password is generated, credentials mirror the options, and with no Infisical client the result truthfully reports `Synced=false` |
| `TestProvisioner_Run_RecordsEngineConfigFacts` | Recorded `DB_HOST`/`DB_PORT` come from the engine config that was actually dialed (pinned with non-default port 5433) — never hardcoded defaults |
| `TestProvisioner_Run_Overrides` | Every override flag (`--db`, `--user`, `--pass`, `--host`, `--port`) reaches the engine verbatim, and a MySQL app carries no schema |
| `TestProvisioner_Run_RejectsSchemaForMySQL` | `--schema` + MySQL fails **before** `DB.Provision` is ever called |
| `TestSanitizeAppName` (3 cases) | Hyphen → underscore mapping, including multi-hyphen names |

### `internal/services` — Infisical-backed lifecycle (via the `SecretStore` fake)

The `SecretStore` port makes every Infisical-backed flow unit-testable with an in-memory
fake supporting per-operation error injection:

| Test | What it proves |
|---|---|
| `TestProvisioner_Run_ReturnsCredentialsOnSyncFailure` | **The Phase 0 contract:** when the DB is created but the sync fails, `Run` returns the result *alongside* the error — the only copy of the generated password is surfaceable, never lost |
| `TestProvisioner_Run_FailsFastOnRecordedApp` | Pre-flight: an app with recorded contract secrets is refused **before any database mutation** |
| `TestProvisioner_Run_FailsFastOnFolderError` | Pre-flight: a broken secret store (wrong `--env`, outage) fails while there is still nothing to clean up — previously this produced an orphaned database |
| `TestProvisioner_ResolveApp` (3 subtests) | Source-of-truth resolution: not-found and incomplete-secrets errors, recorded names returned verbatim |
| `TestProvisioner_Deprovision` (2 subtests) | Cleanup order (DB drop → each secret → folder) and `database deleted, but failed to …` partial-failure naming |

### `internal/services` — rotation (`rotate_test.go`)

| Test | What it proves |
|---|---|
| `TestProvisioner_Rotate_GeneratesAndRecordsPassword` | Happy path: the *recorded* user rotates with a fresh 32-hex password; database and Infisical hold the **same** value; the existing secret is updated in place (not re-created) |
| `TestProvisioner_Rotate_HonoursOverride` | `--pass` reaches the database verbatim |
| `TestProvisioner_Rotate_RequiresInfisical` | No Infisical → no database mutation, hard error |
| `TestProvisioner_Rotate_RollsBackOnSyncFailure` | **The consistency contract:** a failed `DB_PASSWORD` update triggers a second `RotatePassword` restoring the *old* password — a failed rotation changes nothing |
| `TestProvisioner_Rotate_SurfacesPasswordWhenRollbackFails` | The last-resort path: when apply succeeded but both the secret update *and* the rollback failed, the result carries the live new password so the caller can print the only copy |
| `TestProvisioner_Rotate_RecreatesMissingPasswordSecret` | Recovery use-case: a deleted `DB_PASSWORD` doesn't block rotation — a fresh secret is created |
| `TestProvisioner_Rotate_BackfillsLegacyType` | Legacy apps rotated via `--type` yield credentials carrying the resolved engine (so the verification dial works) |

### `internal/services` — reconciliation & adoption (`syncplan_test.go`, `adopt_test.go`)

| Test | What it proves |
|---|---|
| `TestPlanSecretSync_AllNew` | Fresh app → creates only, in the contract's deterministic order |
| `TestPlanSecretSync_AllUnchanged` | Converged app → a no-op plan (no rewrites of identical values) |
| `TestPlanSecretSync_Mixed` | Crash-mid-sync remnant → correct create/update/unchanged split |
| `TestPlanSecretSync_DeletesStaleContractKeys` | A stale `DB_SCHEMA` (from an app's postgres past) is planned for deletion — otherwise every reader rejects the app |
| `TestPlanSecretSync_PreservesOperatorKeys` | **Safety boundary:** keys outside the contract are operator-owned and never deleted |
| `TestProvisioner_Run_Adopt_ReconcilesRecordedApp` | State A/B recovery: adopt reaches the adapter, exactly the changed facts are rewritten (1 update, 6 unchanged), operator keys survive, DB and Infisical agree on the fresh password |
| `TestProvisioner_Run_Adopt_TypeMismatchRejected` | Adoption never converts engines — recorded `mysql` + `--type postgres` is refused **before any database mutation** |
| `TestProvisioner_Run_Adopt_UpgradesLegacyApp` | A legacy app (no `DB_TYPE`) adopts cleanly and gains the full contract |
| `TestProvisioner_Run_Adopt_HealsMissingDatabase` | State C recovery: database recreated, recorded password updated to the new reality |

### What is deliberately *not* unit-tested

Live SQL execution — the actual `CREATE`/`GRANT`/`DROP`/`ALTER` statements, including the
adopt-mode existence checks (`pg_roles` / `mysql.user`) and ownership convergence — needs
real servers; it's covered by the smoke-test procedure in `HARDENING_PLAYBOOK.md` (Docker
Postgres + MySQL, offline provisioning mode). A build-tagged `testcontainers-go`
integration suite remains the highest-value addition (see the gap table).

---

## Missing features, nice-to-haves & known limitations

An honest gap analysis of the current tree.

### Missing — worth adding

| Gap | Detail |
|---|---|
| **Version information** | No `--version`/`version` command and no `-ldflags` version stamping in the Makefile. Hard to know what build is deployed. |
| **CI pipeline** | No `.github/workflows` — tests/vet/race run only locally. A minimal `go test -race ./...` + `go vet` workflow would protect the invariants the registers fought for. |
| **LICENSE is empty** | The `LICENSE` file is 0 bytes, i.e. the project is effectively unlicensed. Pick one (MIT/Apache-2.0) and fill it in. |
| **Postgres connect timeout** | The MySQL admin dial has a 10 s timeout; the Postgres DSN sets none (`connect_timeout` absent), so a black-holed host hangs until Ctrl-C. One-line fix in `dsn()`. |
| **CA verification story** | Postgres `verify-ca`/`verify-full` work but there's no `sslrootcert` config key (workaround: lib/pq honours libpq env vars like `PGSSLROOTCERT`). MySQL's `mysqlTLSParam` passes unknown values through as a "registered custom TLS config" — but nothing ever calls `mysql.RegisterTLSConfig`, so that branch is currently unreachable in practice. |
| **Integration tests** | The SQL-execution paths (create/harden/drop, rollback under injected failures) would fit `testcontainers-go` nicely and are today verified only by hand. |

### Nice to have

| Idea | Why |
|---|---|
| `--dry-run` on `provision`/`delete` | Print the derived names, target host, and SQL plan without touching anything — great for prod confidence. |
| Read-only / extra users per app | Both engines currently grant the single app user `ALL`. A `--read-only-user` companion account is a common need (dashboards, analytics). |
| JSON log mode | Logs are pretty for humans; a `--log-format json` would suit CI and log aggregation. |
| `provision --format json` | Machine-readable provisioning output (esp. the offline credential dump, currently `KEY=VALUE` lines). |
| Environment discovery | `list` requires you to know the env slug; enumerating available Infisical environments would help. |
| Password shape options | Generated passwords are hex-only (128-bit — strong, but some password policies demand symbols). A `--pass-length`/charset option would cover that. |
| Release engineering | `goreleaser` config, prebuilt binaries, Homebrew tap, `cobra` man-page/markdown docs generation. |
| Delete prompt shows the host | The confirmation names db/user/app/env but not the server — a wrong-cluster foot-gun when multiple configs float around. |
| Richer `test` | Beyond ping: verify schema access (`SELECT current_schema()`), round-trip a temp table, report server version/TLS cipher in verbose mode. |
| Windows shell quoting | `shellQuote` targets POSIX shells; the `psql`/`mysql` formats aren't safe to paste into `cmd.exe`/PowerShell. |

### Known limitations (by design, documented)

- **Two engines only** (`postgres`, `mysql`). The registry makes adding one cheap: an entry
  in `engineFactories`, a `Database` implementation, a `config.Engine` case, and a
  `ConnectionStringGenerator` branch.
- **Legacy apps without `DB_TYPE`** can be deleted (`--type` fallback) but not used with
  `conn`/`test` — the error message says to re-provision.
- **`mysql.database_user_host` must stay stable** — `delete` drops `'user'@'<current
  config>'`, so changing it orphans accounts created under the old value.
- **Masking is a display default, not access control** — `--show-values`, `--copy`, and the
  non-table `conn` formats intentionally reveal secrets to the operator, who holds the
  Infisical identity anyway.
- **Secret history hygiene:** per `ARCHITECTURE.md` D26, credentials once committed to git
  history require rotation/repo migration — a process action, not a code fix.
- **One Infisical layout:** secrets always live at `/<app>` in the given environment; the
  path isn't configurable.

---

## Development

```bash
make build     # go build -o databasemanager main.go
make test      # go test ./...
make clean     # remove the binary
make           # test + build

# extras the Makefile doesn't wrap yet:
go vet ./...
go test -race -count=1 ./...
```

### Project layout

```
.
├── main.go                     # composition root stage 1: signal ctx, logger, container
├── cmd/                        # Cobra commands (stage 2: config + Infisical wiring)
│   ├── root.go                 #   persistent flags, TLS warning helper
│   ├── provision.go            #   provision + its 8 flags
│   ├── delete.go               #   resolve-confirm-deprovision
│   ├── list.go                 #   table/JSON rendering + masking
│   ├── conn.go                 #   ConnectionStringGenerator, shellQuote, masking
│   └── test.go                 #   app-credential connectivity check
├── internal/
│   ├── app/                    # dependency container (immutable after wiring)
│   ├── config/                 # viper load + validation, env binding
│   ├── database/               # engine registry, adapters, Credentials contract
│   │   ├── factory.go          #   engine registry + New()
│   │   ├── database.go         #   Database interface, ProvisionOptions + validation
│   │   ├── credentials.go      #   the secret contract (single owner)
│   │   ├── postgres.go         #   Postgres adapter (DSN quoting, lockdown, rollback)
│   │   └── mysql.go            #   MySQL adapter (SQL-mode-aware escaping, TLS mapping)
│   ├── infisical/              # client construction (Universal Auth)
│   ├── logger/                 # styled stderr logger
│   └── services/               # Provisioner (lifecycle) + password generation
├── ARCHITECTURE.md             # as-built architecture + defect/hardening registers
├── HARDENING_PLAYBOOK.md       # hands-on security-fix walkthrough
└── Makefile
```

### Keep these invariants (from `ARCHITECTURE.md` §11)

When contributing, the short list that reviewers will hold you to: stdout is data / stderr
is chrome; the secret contract is owned solely by `database.Credentials`; delete resolves
identity from Infisical, never by convention; every identifier passes `validateIdentifier`
on both provision *and* delete paths; every literal goes through the engine's quoting
helper; TLS stays on by default; masking is by construction, not pattern-matching.
