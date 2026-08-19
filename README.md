# databasemanager

A command line tool that creates a database and a matching user for an
application, and stores the resulting credentials in Infisical instead of
handing them to you on the terminal. It also reads those credentials back, so
the same tool can print a connection string, test that the credentials work,
rotate the password, and remove everything when the application is retired.

Written in Go. Works with PostgreSQL and MySQL.


## Why I wrote this

I run a small homelab with a handful of services on it. Most of them need a
database. For a long time I did the obvious thing: SSH into the Postgres box,
`CREATE DATABASE`, `CREATE USER`, copy the password into a `.env` file on
whichever machine needed it, and move on.

That worked until it did not. After a year I had:

- No reliable list of which databases existed or what used them. Some were
  left over from services I had already deleted.
- Passwords copied into `.env` files, a compose file, one or two shell
  histories, and a note on my phone. Rotating one meant hunting for all the
  copies, and I usually did not bother.
- Several services sharing one database and one user, because creating a
  separate one every time was tedious. One compromised service meant all of
  the data was reachable.
- Half finished setups. I would create the database, get distracted, and come
  back later unsure whether the user existed or what its password was.

I was already running Infisical for other secrets, so the fix was to make
Infisical the single place the credentials live, and to make creating a
database a single command that either finishes completely or leaves nothing
behind. That is what this does.

The name of the game is that after `provision`, nobody, including me, ever
sees the password unless they explicitly ask for it. It is generated, applied
to the database, written to Infisical, and forgotten.


## What it does

    $ databasemanager provision billing-api --type postgres --env dev
    INFO  starting provisioning app=billing-api type=postgres
    INFO  provisioning database name=billing_api_db user=billing_api_user
    INFO  syncing secrets to infisical env=dev
    INFO  Successfully provisioned app=billing-api db_name=billing_api_db
          secrets="7 created, 0 updated, 0 deleted, 0 unchanged"

That single command:

1. Works out a database name and user name from the application name.
2. Creates the user with a random 32 character password.
3. Creates the database owned by that user.
4. Takes away the default permissions that Postgres and MySQL hand out.
5. Writes the connection details into Infisical under a folder named after
   the application.

If any of those steps fails, it drops whatever it already created before
returning the error. You do not end up with a database and no user, or a user
and no recorded password.

Later on:

    $ databasemanager list
    +-------------+
    | APP NAME    |
    +-------------+
    | billing-api |
    +-------------+

    $ databasemanager conn billing-api --format uri
    postgresql://billing_api_user:8f3c...@192.168.1.20:5432/billing_api_db?search_path=public

    $ databasemanager test billing-api
    INFO  connection successful!

    $ databasemanager rotate billing-api
    INFO  password applied to database user user=billing_api_user
    INFO  DB_PASSWORD updated in Infisical app=billing-api
    INFO  verified: app credentials connect successfully

    $ databasemanager delete billing-api
    INFO  Successfully deleted app=billing-api


## Requirements

- Go 1.25 or later to build it.
- An Infisical instance, either the hosted one or self hosted. I run self
  hosted.
- A PostgreSQL or MySQL server, and an admin account on it that can create
  databases and users. On Postgres that means `CREATEDB` and `CREATEROLE`. On
  MySQL it means `CREATE USER` and `GRANT`.


## Building

    git clone https://github.com/pratiknkulkarni/databasemanager
    cd databasemanager
    make build

That leaves a `databasemanager` binary in the current directory. There is
nothing else to install and no runtime dependencies.

If you would rather not clone it, and you have Go installed:

    go install github.com/pratiknkulkarni/databasemanager@latest

Either way, `databasemanager version` prints what you ended up with:

    databasemanager v1.0.0
      commit:  1f0c3a0e...
      built:   2026-08-19T04:22:05Z
      go:      go1.26.5
      platform: linux/amd64

`make build` stamps the version, commit and build date in through the linker.
A `go install` has no linker flags, so it reads the module version and the VCS
stamps the toolchain records instead, and prints those. `version` is also the
one command that does not load the config file or reach for Infisical, so it
still answers on a machine that is not set up yet.


## Configuration

Configuration comes from `.databasemanager.yaml`, looked for in the current
directory and then in your home directory. You can point at a different file
with `--config`. Every setting can also be given as an environment variable
with a `DATABASEMANAGER_` prefix, so the tool runs with no config file at all
if you would rather keep everything in the environment.

    infisical_project_id: "your-project-id"
    infisical_site_url: "https://infisical.example.com"

    # Universal Auth. The client re-authenticates on every run.
    infisical_client_id: "machine-identity-client-id"
    infisical_client_secret: "machine-identity-client-secret"

    # Or Token Auth, if you would rather paste a pre-issued token.
    # Use this instead of the pair above. Note that these tokens expire for
    # good at their max TTL, 30 days by default, and have to be reissued.
    # infisical_access_token: "eyJhbGciOi..."

    postgres:
      database_hostname: "192.168.1.20"
      database_port: 5432
      database_user: "postgres"
      database_password: "..."
      database_name: "postgres"     # the maintenance DB the admin dial uses
      # database_sslmode: "require"

    mysql:
      database_hostname: "db.internal"
      database_port: 3306
      database_user: "root"
      database_password: "..."
      database_name: "mysql"
      # database_user_host: "10.0.%"

Both engine sections are optional. If you only run Postgres, leave the MySQL
section out entirely and you will only get an error if you actually ask for a
MySQL database.

Two things that cost me time when I first set this up, so they are worth
saying plainly:

**The machine identity has to be added to the project, not just the
organisation.** Infisical checks project membership separately. If you skip
it, authentication succeeds and then every call comes back with a 403 saying
you are not a member of the project. Add it under Access Control inside the
project itself.

**Do not paste a Token Auth token into `infisical_client_secret`.** They look
similar enough to mix up, and the server just returns a 401 with no
explanation. The tool now checks for this at startup and tells you which field
to use.


## Commands

### provision

    databasemanager provision <app> [--type postgres|mysql] [--env dev]

Creates the database, the user, and the secrets. Useful flags:

    --db, --user, --pass       override the generated names or password
    --host, --port             override what gets recorded, if the address
                               apps should use differs from the one the
                               admin connection uses
    --schema                   Postgres only, defaults to public
    --adopt                    reconcile instead of refusing, see below
    --force                    skip the confirmation prompt for --adopt

Running `provision` twice on the same application is refused, so you cannot
overwrite a working setup by accident. If you actually want to converge on
existing state, use `--adopt`:

    $ databasemanager provision billing-api --adopt --force
    INFO  adopted existing database name=billing_api_db
    INFO  adopted existing user (password reset) user=billing_api_user
    INFO  Successfully provisioned db_created=false user_created=false
          secrets="0 created, 1 updated, 0 deleted, 6 unchanged"

This is the command that fixes the half finished setups I mentioned at the
top. It keeps whatever already exists, resets the password so it is known
again, reapplies the permission changes, and writes any missing secrets. You
can run it against a database you created by hand years ago and it will bring
it under management. It is safe to run repeatedly.

### delete

    databasemanager delete <app> [--force]

Drops the database and the user, then removes the secrets and the folder.

It reads the database and user names out of Infisical rather than working them
out from the application name again. That matters. If somebody had passed
`--db` at provision time, guessing the name would drop the wrong database, or
more likely nothing at all while reporting success.

### list

    databasemanager list [--env dev]

Prints the applications that have secrets recorded in that environment. This
is the inventory I did not have before.

### conn

    databasemanager conn <app> [--format table|uri|psql|mysql|env] [--copy]

Prints connection strings. The default table view masks the password, so you
can run it while screen sharing. Ask for a specific format and you get the
real value, since at that point you clearly meant to. `--copy` puts it on the
clipboard instead of the screen.

### test

    databasemanager test <app>

Pulls the credentials out of Infisical and actually opens a connection with
them. This answers "are the stored credentials still correct", which is
different from "is the database up".

### rotate

    databasemanager rotate <app> [--force] [--no-verify]

Issues a new password, applies it to the database, then updates Infisical.

The order matters and the failure case is handled. If the database change
succeeds but Infisical is then unreachable, the old password is put back
before returning the error. Otherwise you would be left with a live password
that nothing has a record of, which is a locked out application. After a
successful rotation it dials the database with the new credentials to confirm,
unless you pass `--no-verify`.

Existing connections are not affected, since passwords are only checked when a
connection is opened. Anything that reconnects has to pick up the new secret.

Rotate also works as recovery. If `DB_PASSWORD` was deleted or lost, rotating
issues a fresh one and records it, rather than failing because it could not
read the old one.


## What ends up in Infisical

Secrets go in a folder named after the application, at the path `/<app>`, in
whichever environment you passed with `--env`. Seven keys for Postgres, six
for MySQL:

    DB_TYPE       postgres or mysql
    DB_NAME       billing_api_db
    DB_USER       billing_api_user
    DB_PASSWORD   generated, 32 hex characters
    DB_HOST       192.168.1.20
    DB_PORT       5432
    DB_SCHEMA     public          (Postgres only)

`DB_TYPE` is what lets later commands work without you telling them which
engine an application uses. `conn`, `test`, `rotate` and `delete` all read it
first and pick the right adapter.

The names are derived from the application name: dashes become underscores and
anything else unsuitable is dropped, then `_db` and `_user` are appended. So
`billing-api` gives `billing_api_db` and `billing_api_user`. You can override
either at provision time, and since the real names are recorded in Infisical,
everything afterwards uses the recorded value rather than recomputing it.


## Notes on security

This was most of the point of the project, so it is worth listing what it
actually does rather than just saying it is secure.

**Every application gets its own database and its own user.** No sharing.

**Default permissions are taken away.** On Postgres, a freshly created
database lets any role connect to it, and until recently the `public` schema
was writable by anyone. Both are revoked, and the schema is owned by the
application user. On MySQL, the grant is scoped to that one database rather
than being global, and the account's host part can be restricted with
`database_user_host` so it is not reachable from anywhere on the network.

**Passwords come from `crypto/rand`.** Sixteen random bytes, hex encoded. If
the entropy source cannot be read, provisioning fails rather than falling back
to something weaker.

**Identifiers are quoted, and validated before that.** Database and user names
go through the engine's own quoting function, and are checked against a
whitelist first, so an application name cannot smuggle SQL through.

**Failures roll back.** Provisioning tracks what it created and drops it if a
later step fails. The rollback deliberately runs on its own fresh 10 second
context, so hitting Ctrl-C partway through still cleans up rather than
cancelling the cleanup too.

**Passwords are masked by default in output.** You have to ask for the plain
value.

**TLS is on unless you turn it off.** Postgres defaults to `require` and MySQL
to an encrypted connection. If you set `database_sslmode: disable`, every
single command prints a warning that credentials are travelling in cleartext.
My own Postgres box does not have TLS enabled yet, so I see that warning a
lot, which is the intended effect.

One thing this does not do is protect the admin credentials in the config
file. That file holds the password to your database server, and anyone who can
run the tool holds the Infisical machine identity anyway. Keep it out of git
and readable only by you.


## How it is put together

    main.go                signal handling, logger, dependency container
    cmd/                   one file per command, flag parsing, output
    internal/app/          the dependency container
    internal/config/       config loading and validation
    internal/database/     engine adapters and the secret contract
    internal/infisical/    client construction and authentication
    internal/services/     the provisioning lifecycle
    internal/logger/       logger setup

A few decisions worth explaining:

**Adding an engine means adding one file.** `internal/database` has a registry
and an interface. Postgres and MySQL are two implementations of it. Nothing in
`cmd` or `internal/services` knows which engine it is dealing with, apart from
the schema handling, which is genuinely Postgres specific.

**The secret store is behind a small interface** defined where it is used
rather than next to the SDK adapter. It has seven methods covering secrets and
folders. That keeps the Infisical SDK out of the provisioning logic and makes
the tests readable, since faking seven methods takes a few lines and does not
need a live server.

**Infisical being down is a warning, not a crash.** Commands that need secrets
fail properly. `provision` carries on without it and prints the generated
credentials to the terminal, because at that point the credentials exist on
the database server and that printout is the only copy. Losing them silently
would be worse than printing them.

**One invocation, one process, one goroutine.** There is no daemon, no state
file, no local cache. Infisical is the state. Restarting from any point works
because there is no local state to get out of sync.


## Tests

    make test

59 test functions, covering the provisioning lifecycle, the adopt path, the
rotation rollback, the secret reconciliation, config loading and validation,
and the SQL generation for both engines. Everything runs against fakes, so the
suite needs no database and no network and finishes in well under a second.

There is also a smoke test script for exercising a real setup end to end. It
provisions, lists, connects, tests, rotates, adopts, checks that the negative
cases fail properly, and deletes, cleaning up after itself even if it fails
partway.


## Limitations

Things I know are missing, in roughly the order they annoy me:

- Only PostgreSQL and MySQL. SQLite would not fit the model. MongoDB might.
- No `import` command for bulk adopting a lot of existing databases at once.
  You can adopt them one at a time with `--adopt`.
- Nothing schedules rotation. `rotate` is a single command, so cron handles it
  fine, but there is no built in policy or expiry tracking.
- No backup or restore. Deliberately, that is a different job.
- No dry run flag on `provision` or `delete`.
- Read only or per schema users are not supported. One database, one user,
  full rights on that database, nothing else.


## Other documents

- [`REFERENCE.md`](REFERENCE.md) covers every flag, secret key, output format
  and exit code, if you want the exhaustive version of the command list above.
- [`ARCHITECTURE.md`](ARCHITECTURE.md) describes how the packages fit together
  and which invariants each boundary is holding.
- [`HARDENING_PLAYBOOK.md`](HARDENING_PLAYBOOK.md) walks through exercising
  each of the security behaviours yourself, against a real server.

---

Developed on a self-hosted [Gitea](https://gitea.15092021.xyz/pratik/databasemanager) that runs in
my homelab; the copy on GitHub is a read-only mirror of it, pushed on every commit.
Issues and pull requests are welcome on the GitHub side and I will port them across.
