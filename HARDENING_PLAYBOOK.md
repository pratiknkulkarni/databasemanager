# Hardening Playbook — how to test & play with the 2026-07-05 changes

This walks through exercising every security fix from the hardening pass. Each fix has a
**fast path** (a targeted unit test that proves the logic in milliseconds, no server needed)
and, where it's observable, a **live path** (drive the real CLI against a local database).

Fixes map to `ARCHITECTURE.md` §13: **S1** TLS, **S2** delete-path validation + MySQL
escaping, **S3** DSN quoting, **S4** URI/DSN encoding, **S5** MySQL user-host scoping,
**S6** `conn` masking + shell-safety, **S7** `list` masking.

---

## 0. Prerequisites

```bash
cd /path/to/databasemanager
go build ./...      # must be clean
go test ./...       # must be all green
```

> **Note on Infisical.** `provision` runs **offline** when Infisical is unreachable — it
> prints the generated credentials to stdout — so you can exercise the provisioning + TLS +
> SQL-construction paths against a local DB with only dummy Infisical config. `delete`,
> `conn`, `test`, and `list` genuinely need a reachable Infisical project. For those, the
> fast (unit) path below proves the changed logic without a server.

---

## 1. Fast path — prove every fix with unit tests

```bash
# S1  TLS defaults + mapping, and Postgres DSN quoting/secure-default
go test ./internal/database -run 'TestMySQLTLSParam|TestPostgresDSN' -v

# S2  delete-path identifier gate (both engines) + SQL-mode-aware escaping
go test ./internal/database -run 'Delete_RejectsBadIdentifiers|escapeLiteral' -v

# S4  credential URIs are percent-encoded (no userinfo breakout)
go test ./cmd -run 'TestGenerate_SpecialCharPasswordIsEncoded' -v

# S6  masking is leak-proof; psql/mysql command output is shell-safe
go test ./cmd -run 'TestMaskByRegeneration|TestShellQuote|TestGeneratePSQL_ShellSafe' -v

# S7  list masks passwords by key AND inside connection-URI values
go test ./cmd -run 'TestMaskSecretValue' -v
```

Each test's name and comment states the exact attack it closes. For example
`TestPostgresDSN_QuotesValuesAndDefaultsSecure` feeds an admin password of
`x host=evil.example` and asserts the DSN quotes it (`password='x host=evil.example'`) instead
of letting it inject a second `host=` keyword.

---

## 2. Live path — spin up local databases

Stock Postgres ships **without** TLS; stock MySQL 8 ships **with** a self-signed cert. That
difference is exactly what makes the TLS demo instructive.

```bash
docker run -d --name hp-pg   -e POSTGRES_PASSWORD=adminpass -p 5432:5432 postgres:16
docker run -d --name hp-mysql -e MYSQL_ROOT_PASSWORD=rootpass -p 3306:3306 mysql:8
```

Create `./.databasemanager.yaml` (the Infisical values can be dummy — provisioning falls back
to offline mode and prints the credentials):

```yaml
infisical_project_id: "dummy"
infisical_client_id: "dummy"
infisical_client_secret: "dummy"
infisical_site_url: "https://app.infisical.com"

postgres:
  database_hostname: "127.0.0.1"
  database_port: 5432
  database_user: "postgres"
  database_password: "adminpass"
  database_name: "postgres"
  # database_sslmode intentionally omitted → secure default (require)

mysql:
  database_hostname: "127.0.0.1"
  database_port: 3306
  database_user: "root"
  database_password: "rootpass"
  database_name: "mysql"
  # database_sslmode omitted → secure default (skip-verify)
  database_user_host: "%"
```

Build the binary once: `go build -o dbm .`

---

## 3. S1 — TLS is on by default (and disabling it is loud)

**Secure default rejects a cleartext server.** Against stock (non-TLS) Postgres:

```bash
./dbm provision demo-app --type postgres --env dev
# → fails: the admin dial uses sslmode=require and the server offers no TLS.
#   This is the fix working: before, it would have connected in cleartext.
```

**Opt out explicitly for local dev** — add to the `postgres:` section and re-run:

```yaml
  database_sslmode: "disable"
```

```bash
./dbm provision demo-app --type postgres --env dev
# → WARN databasemanager: TLS is DISABLED for this connection — credentials travel in cleartext  engine=postgres
# → provisions, then prints DB_* credentials (offline: no Infisical).
```

The warning is the point: cleartext is never silent. MySQL 8's self-signed cert means the
MySQL path *succeeds* on the default `skip-verify` — flip `database_sslmode: disable` on the
`mysql:` section to see the same warning there.

---

## 4. S3/S4/S5 — injection-safe construction, observable live

With `database_sslmode: disable` set for local dev:

**S5 — user-host scoping.** Set `database_user_host: "127.0.0.1"` on the `mysql:` section,
provision a MySQL app, then inspect the account MySQL actually created:

```bash
./dbm provision mysql-demo --type mysql --env dev
docker exec -it hp-mysql mysql -uroot -prootpass \
  -e "SELECT user, host FROM mysql.user WHERE user LIKE 'mysql_demo%';"
# host column shows 127.0.0.1, not % — the account is no longer reachable network-wide.
```

**S3/S4 — quoting/encoding.** These defend against values that carry connection-parameter or
URI metacharacters. The safe cases are proven by unit tests (§1); to see them live, provision
with an override password full of metacharacters and confirm it round-trips instead of
breaking the dial:

```bash
./dbm provision weird-pass --type mysql --env dev --pass 'p@ss/w:rd?x&y'
# Provisioning succeeds: the driver-built DSN escapes the password rather than misparsing it.
```

---

## 5. S2 — `delete` rejects tampered names (no server needed)

The delete path re-validates the `DB_NAME`/`DB_USER` it reads back from Infisical **before**
any SQL or connection. You can prove the gate fires ahead of the dial with the unit tests:

```bash
go test ./internal/database -run 'Delete_RejectsBadIdentifiers' -v
```

Both `TestMySQLDelete_RejectsBadIdentifiers` and `TestPostgresDelete_RejectsBadIdentifiers`
call `Delete` with a name like `db'; DROP DATABASE prod; --` on a client with **no**
connection and assert it returns an `invalid characters` error — i.e. a malicious Infisical
secret is refused before it can reach the database. The SQL-mode-aware escaping that backs
this up is pinned by `TestMySQLClient_escapeLiteral` (doubles quotes under
`NO_BACKSLASH_ESCAPES`).

---

## 6. S6/S7 — masking (fast path is the real proof)

`conn` masking now works by regenerating the string with a marker password, so it cannot leak
tails on awkward characters:

```bash
go test ./cmd -run 'TestMaskByRegeneration' -v
```

To eyeball it against a real app you need Infisical-stored secrets, then:

```bash
./dbm conn demo-app --env dev --format table   # passwords rendered as *****
./dbm list demo-app --env dev                  # DB_PASSWORD masked; a DB_URI-style value has only its password masked
```

`--show-values` on `list` still reveals everything on demand — masking is a display default,
not access control.

---

## 7. Cleanup

```bash
docker rm -f hp-pg hp-mysql
rm -f ./.databasemanager.yaml ./dbm
```

---

## What still needs a real environment

- Full `delete` / `conn` / `test` / `list` end-to-end requires a live Infisical project;
  their **changed logic** is unit-covered above.
- Actual SQL execution against the server (the create/grant/drop statements) is verified by
  smoke test, not unit tests — spin up the Docker DBs in §2 and provision offline to drive it.
- The git-history credential exposure (audit CRITICAL-1) is **not** a code change: it is
  resolved by moving to a fresh repo and rotating the leaked Infisical + DB secrets.
```
