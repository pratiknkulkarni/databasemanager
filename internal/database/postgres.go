package database

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/praaatik/databasemanager/internal/config"
)

// defaultPostgresSSLMode is the transport-security posture used when the
// configuration does not set one: encryption is required, defeating passive
// interception, without demanding a verifiable certificate chain.
const defaultPostgresSSLMode = "require"

// Dial budgets. These bound the wait on an unreachable server, which would
// otherwise be the OS TCP timeout — minutes, or unbounded against a host that
// silently drops packets. The admin dial gets the longer budget because it
// runs once per invocation; the app-credential check is a connectivity probe
// and should report failure promptly. Both mirror the MySQL adapter, which
// already sets these on its driver config.
const (
	connectTimeout    = 10 * time.Second
	appConnectTimeout = 5 * time.Second
)

type PostgresClient struct {
	cfg config.DatabaseConfig
	db  *sql.DB
}

func newPostgresClient(cfg config.DatabaseConfig) *PostgresClient {
	return &PostgresClient{cfg: cfg}
}

// sslMode resolves the effective libpq sslmode, defaulting to a secure value.
func (p *PostgresClient) sslMode() string {
	if p.cfg.DatabaseSSLMode != "" {
		return p.cfg.DatabaseSSLMode
	}
	return defaultPostgresSSLMode
}

// quotePostgresDSNValue quotes a value for the libpq keyword/value DSN format.
// Without this, a value containing a space or an embedded `key=value` pair
// would be parsed as additional connection parameters — e.g. a password of
// `x host=evil` would silently redirect the admin dial.
func quotePostgresDSNValue(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}

// dsn builds the admin DSN, connecting to the given database name.
//
// connect_timeout bounds the TCP dial. Without it libpq waits on the OS
// default, which against a host that drops packets rather than refusing them
// — a firewalled database, a stale address — means the CLI hangs indefinitely
// instead of reporting that the server is unreachable.
func (p *PostgresClient) dsn(dbName string) string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d",
		quotePostgresDSNValue(p.cfg.DatabaseHostname),
		p.cfg.DatabasePort,
		quotePostgresDSNValue(p.cfg.DatabaseUser),
		quotePostgresDSNValue(p.cfg.DatabasePassword),
		quotePostgresDSNValue(dbName),
		quotePostgresDSNValue(p.sslMode()),
		int(connectTimeout.Seconds()))
}

func (p *PostgresClient) ensureConnection(ctx context.Context) error {
	if p.db != nil {
		return nil
	}

	db, err := sql.Open("postgres", p.dsn(p.cfg.DatabaseName))
	if err != nil {
		return fmt.Errorf("failed to open postgres connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to ping postgres: %w", err)
	}

	p.db = db
	return nil
}

// Close releases the admin connection pool.
func (p *PostgresClient) Close() error {
	if p.db == nil {
		return nil
	}
	err := p.db.Close()
	p.db = nil
	return err
}

func (p *PostgresClient) databaseExists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := p.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (p *PostgresClient) userExists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := p.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1)", name).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (p *PostgresClient) Provision(ctx context.Context, opts ProvisionOptions) (report ProvisionReport, err error) {
	if err := opts.Validate(); err != nil {
		return report, fmt.Errorf("invalid provision options: %w", err)
	}

	if err := p.ensureConnection(ctx); err != nil {
		return report, err
	}

	dbExists, err := p.databaseExists(ctx, opts.DatabaseName)
	if err != nil {
		return report, fmt.Errorf("failed to check if database exists: %w", err)
	}
	userExists, err := p.userExists(ctx, opts.DatabaseUser)
	if err != nil {
		return report, fmt.Errorf("failed to check if user exists: %w", err)
	}
	if !opts.Adopt {
		if dbExists {
			return report, fmt.Errorf("database %q already exists (re-run with --adopt to converge it)", opts.DatabaseName)
		}
		if userExists {
			return report, fmt.Errorf("user %q already exists (re-run with --adopt to converge it)", opts.DatabaseUser)
		}
	}

	// The state machine records only what THIS run creates, so the deferred
	// rollback never drops adopted (pre-existing) resources.
	state := &provisionState{}
	defer func() {
		if err != nil {
			p.rollbackProvision(opts.DatabaseName, opts.DatabaseUser, state)
		}
	}()

	userStr := pq.QuoteIdentifier(opts.DatabaseUser)
	dbStr := pq.QuoteIdentifier(opts.DatabaseName)

	// 1. User: create, or adopt by applying the known password — the
	// existing one is unrecoverable (only hashes are stored server-side).
	if userExists {
		if err = p.RotatePassword(ctx, opts.DatabaseUser, opts.DatabasePassword); err != nil {
			return report, err
		}
	} else {
		_, err = p.db.ExecContext(ctx, fmt.Sprintf("CREATE USER %s WITH PASSWORD %s", userStr, pq.QuoteLiteral(opts.DatabasePassword)))
		if err != nil {
			return report, fmt.Errorf("failed to create user: %w", err)
		}
		state.userCreated = true
		report.UserCreated = true
	}

	// 2. Database: create, or adopt by converging ownership onto the app user.
	if dbExists {
		if _, err = p.db.ExecContext(ctx, fmt.Sprintf("ALTER DATABASE %s OWNER TO %s", dbStr, userStr)); err != nil {
			return report, fmt.Errorf("failed to converge database owner: %w", err)
		}
	} else {
		_, err = p.db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", dbStr, userStr))
		if err != nil {
			return report, fmt.Errorf("failed to create database: %w", err)
		}
		state.databaseCreated = true
		report.DatabaseCreated = true
	}

	// 3. HARDENING: Connect to the new DB to lock it down. Every statement
	// is idempotent, so re-applying on an adopted database is safe.
	if err = p.lockdownDatabase(ctx, opts); err != nil {
		return report, fmt.Errorf("failed to apply security hardening: %w", err)
	}

	return report, nil
}

// rollbackProvision drops whatever a failed provisioning run created. It uses
// a fresh context because the request context may already be cancelled.
func (p *PostgresClient) rollbackProvision(dbName, userName string, state *provisionState) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if state.databaseCreated {
		_, _ = p.db.ExecContext(cleanupCtx, "DROP DATABASE IF EXISTS "+pq.QuoteIdentifier(dbName))
	}
	if state.userCreated {
		_, _ = p.db.ExecContext(cleanupCtx, "DROP USER IF EXISTS "+pq.QuoteIdentifier(userName))
	}
}

// lockdownDatabase connects to the specific app database to revoke default public privileges
func (p *PostgresClient) lockdownDatabase(ctx context.Context, opts ProvisionOptions) error {
	appDB, err := sql.Open("postgres", p.dsn(opts.DatabaseName))
	if err != nil {
		return err
	}
	defer func() { _ = appDB.Close() }()

	if err := appDB.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to connect to new db for hardening: %w", err)
	}

	// 3a. Revoke PUBLIC Connect on the Database
	if _, err := appDB.ExecContext(ctx, "REVOKE CONNECT ON DATABASE "+pq.QuoteIdentifier(opts.DatabaseName)+" FROM PUBLIC"); err != nil {
		return fmt.Errorf("failed to revoke public connect: %w", err)
	}

	// 3b. Isolate Public Schema
	adminUser := pq.QuoteIdentifier(p.cfg.DatabaseUser)

	// Make Admin the owner of public schema (so the app user can't mess with it easily)
	if _, err := appDB.ExecContext(ctx, fmt.Sprintf("ALTER SCHEMA public OWNER TO %s", adminUser)); err != nil {
		return fmt.Errorf("failed to change public schema owner: %w", err)
	}

	// Revoke all rights on public schema from everyone (PUBLIC)
	if _, err := appDB.ExecContext(ctx, "REVOKE ALL ON SCHEMA public FROM PUBLIC"); err != nil {
		return fmt.Errorf("failed to revoke public schema access: %w", err)
	}

	// 3c. Grant Schema Usage to the App User (explicitly)
	userStr := pq.QuoteIdentifier(opts.DatabaseUser)
	if opts.Schema == "public" {
		if _, err := appDB.ExecContext(ctx, fmt.Sprintf("GRANT ALL ON SCHEMA public TO %s", userStr)); err != nil {
			return fmt.Errorf("failed to grant schema access to user: %w", err)
		}
	} else if opts.Schema != "" {
		// Create the custom schema (or adopt an existing one) and converge
		// its ownership onto the app user.
		schemaStr := pq.QuoteIdentifier(opts.Schema)
		if _, err := appDB.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s AUTHORIZATION %s", schemaStr, userStr)); err != nil {
			return fmt.Errorf("failed to create custom schema: %w", err)
		}
		if _, err := appDB.ExecContext(ctx, fmt.Sprintf("ALTER SCHEMA %s OWNER TO %s", schemaStr, userStr)); err != nil {
			return fmt.Errorf("failed to converge custom schema owner: %w", err)
		}
	}

	return nil
}

// RotatePassword sets a new password for an existing role. Live sessions are
// unaffected: PostgreSQL checks passwords at connection time only.
func (p *PostgresClient) RotatePassword(ctx context.Context, userName, newPassword string) error {
	// The name arrives from Infisical, not the validated provision path —
	// gate it on the identifier allowlist before it reaches any SQL.
	if err := validateIdentifier("database user", userName, maxUserLen); err != nil {
		return err
	}
	if newPassword == "" {
		return fmt.Errorf("new password must not be empty")
	}

	if err := p.ensureConnection(ctx); err != nil {
		return err
	}

	_, err := p.db.ExecContext(ctx, fmt.Sprintf("ALTER USER %s WITH PASSWORD %s",
		pq.QuoteIdentifier(userName), pq.QuoteLiteral(newPassword)))
	if err != nil {
		return fmt.Errorf("failed to rotate password: %w", err)
	}
	return nil
}

func (p *PostgresClient) Delete(ctx context.Context, databaseName, userName string) error {
	// The names arrive from Infisical, not from the validated provision path,
	// so re-apply the identifier allowlist before they reach any SQL. Quoting
	// alone is not the only line of defence.
	if err := validateIdentifier("database name", databaseName, maxIdentifierLen); err != nil {
		return err
	}
	if err := validateIdentifier("database user", userName, maxUserLen); err != nil {
		return err
	}

	if err := p.ensureConnection(ctx); err != nil {
		return err
	}

	// Terminate connections first (best effort).
	killQuery := fmt.Sprintf(`
		SELECT pg_terminate_backend(pg_stat_activity.pid)
		FROM pg_stat_activity
		WHERE pg_stat_activity.datname = %s
		AND pid <> pg_backend_pid();`, pq.QuoteLiteral(databaseName))

	_, _ = p.db.ExecContext(ctx, killQuery)

	if _, err := p.db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pq.QuoteIdentifier(databaseName)); err != nil {
		return fmt.Errorf("failed to drop database: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, "DROP USER IF EXISTS "+pq.QuoteIdentifier(userName)); err != nil {
		return fmt.Errorf("failed to drop user: %w", err)
	}

	return nil
}

// testPostgresAppConnection dials postgres with provisioned app credentials.
// Credentials are carried through url.URL so a password containing '@', '/',
// '?' or '&' cannot break out of the userinfo and smuggle connection
// parameters. sslMode defaults to a secure value when unset.
func testPostgresAppConnection(ctx context.Context, creds Credentials, sslMode string) error {
	if sslMode == "" {
		sslMode = defaultPostgresSSLMode
	}

	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(creds.User, creds.Password),
		Host:   net.JoinHostPort(creds.Host, creds.Port),
		Path:   "/" + creds.Name,
	}
	q := url.Values{}
	q.Set("sslmode", sslMode)
	q.Set("connect_timeout", strconv.Itoa(int(appConnectTimeout.Seconds())))
	u.RawQuery = q.Encode()

	appDB, err := sql.Open("postgres", u.String())
	if err != nil {
		return fmt.Errorf("failed to create connection: %w", err)
	}
	defer func() { _ = appDB.Close() }()

	if err := appDB.PingContext(ctx); err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	return nil
}
