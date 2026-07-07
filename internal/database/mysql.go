package database

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/praaatik/databasemanager/internal/config"
)

// defaultMySQLUserHost is the host part of a provisioned account when the
// configuration does not scope it. '%' means "any host"; operators should set
// database_user_host to a narrower pattern for network isolation.
const defaultMySQLUserHost = "%"

type MySQLClient struct {
	cfg config.DatabaseConfig
	db  *sql.DB

	// noBackslashEscapes records whether the connected server runs with the
	// NO_BACKSLASH_ESCAPES SQL mode, under which '\'' is NOT an escape and the
	// only way to embed a quote in a literal is to double it. Escaping has to
	// follow the server, not assume the default.
	noBackslashEscapes bool
}

func newMySQLClient(cfg config.DatabaseConfig) *MySQLClient {
	return &MySQLClient{cfg: cfg}
}

// escapeMySQLLiteral escapes a value for a single-quoted literal under the
// default (backslash-escaping) SQL mode: backslashes must be escaped as well as
// quotes, otherwise a trailing backslash would escape the closing quote.
func escapeMySQLLiteral(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

// escapeLiteral escapes a value for a single-quoted literal correctly for the
// connected server's SQL mode. Under NO_BACKSLASH_ESCAPES a quote is escaped by
// doubling it and backslashes are ordinary characters; otherwise backslash
// escaping applies.
func (m *MySQLClient) escapeLiteral(s string) string {
	if m.noBackslashEscapes {
		return strings.ReplaceAll(s, `'`, `''`)
	}
	return escapeMySQLLiteral(s)
}

// userHost is the host part of the account MySQL provisions and drops.
func (m *MySQLClient) userHost() string {
	if m.cfg.DatabaseUserHost != "" {
		return m.cfg.DatabaseUserHost
	}
	return defaultMySQLUserHost
}

// mysqlTLSParam maps a configured sslmode onto a go-sql-driver tls value. The
// default requires encryption (without certificate verification, so it works
// with self-signed server certs) instead of silently transmitting credentials
// in cleartext.
func mysqlTLSParam(sslMode string) string {
	switch strings.ToLower(sslMode) {
	case "":
		return "skip-verify"
	case "disable", "false":
		return "false"
	case "require", "skip-verify":
		return "skip-verify"
	case "verify-ca", "verify-full", "true":
		return "true"
	default:
		// Assume a custom TLS config the operator registered themselves.
		return sslMode
	}
}

// adminDSN builds the admin connection string via the driver's own config
// builder, which escapes user and password correctly (a raw fmt.Sprintf DSN
// would break — or worse, misparse — on a password containing '@' or '/').
func (m *MySQLClient) adminDSN() string {
	c := mysqldriver.NewConfig()
	c.User = m.cfg.DatabaseUser
	c.Passwd = m.cfg.DatabasePassword
	c.Net = "tcp"
	c.Addr = net.JoinHostPort(m.cfg.DatabaseHostname, fmt.Sprintf("%d", m.cfg.DatabasePort))
	c.ParseTime = true
	c.Timeout = 10 * time.Second
	c.TLSConfig = mysqlTLSParam(m.cfg.DatabaseSSLMode)
	return c.FormatDSN()
}

// ensureConnection lazily opens the single admin connection (no database
// selected — provisioning and deletion operate across databases).
func (m *MySQLClient) ensureConnection(ctx context.Context) error {
	if m.db != nil {
		return nil
	}

	db, err := sql.Open("mysql", m.adminDSN())
	if err != nil {
		return fmt.Errorf("failed to open mysql connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to ping mysql: %w", err)
	}

	// Learn the server's escaping semantics before building any literal.
	var mode string
	if err := db.QueryRowContext(ctx, "SELECT @@session.sql_mode").Scan(&mode); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to read mysql sql_mode: %w", err)
	}
	m.noBackslashEscapes = strings.Contains(strings.ToUpper(mode), "NO_BACKSLASH_ESCAPES")

	m.db = db
	return nil
}

// Close releases the admin connection pool.
func (m *MySQLClient) Close() error {
	if m.db == nil {
		return nil
	}
	err := m.db.Close()
	m.db = nil
	return err
}

func (m *MySQLClient) Provision(ctx context.Context, opts ProvisionOptions) (err error) {
	if err := opts.Validate(); err != nil {
		return fmt.Errorf("invalid provision options: %w", err)
	}

	if err := m.ensureConnection(ctx); err != nil {
		return err
	}

	exists, err := m.databaseExists(ctx, opts.DatabaseName)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}
	if exists {
		return fmt.Errorf("database %q already exists", opts.DatabaseName)
	}

	state := &provisionState{}
	defer func() {
		if err != nil {
			m.rollbackProvision(opts.DatabaseName, opts.DatabaseUser, state)
		}
	}()

	// Create database
	if err = m.createDatabase(ctx, opts.DatabaseName); err != nil {
		return err
	}
	state.databaseCreated = true

	// Create user
	if err = m.createUser(ctx, opts.DatabaseUser, opts.DatabasePassword); err != nil {
		return err
	}
	state.userCreated = true

	// Grant all privileges
	if err = m.grantDatabasePrivileges(ctx, opts.DatabaseName, opts.DatabaseUser); err != nil {
		return err
	}

	if err = m.flushPrivileges(ctx); err != nil {
		return err
	}

	return nil
}

// quoteMySQLIdentifier escapes a backtick-quoted identifier by doubling
// backticks. This is correct regardless of the server's SQL mode.
func quoteMySQLIdentifier(s string) string {
	return strings.ReplaceAll(s, "`", "``")
}

func (m *MySQLClient) createDatabase(ctx context.Context, dbName string) error {
	query := fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", quoteMySQLIdentifier(dbName))
	_, err := m.db.ExecContext(ctx, query)
	return err
}

func (m *MySQLClient) createUser(ctx context.Context, userName, password string) error {
	query := fmt.Sprintf("CREATE USER '%s'@'%s' IDENTIFIED BY '%s'",
		m.escapeLiteral(userName), m.escapeLiteral(m.userHost()), m.escapeLiteral(password))
	_, err := m.db.ExecContext(ctx, query)
	return err
}

func (m *MySQLClient) grantDatabasePrivileges(ctx context.Context, dbName, userName string) error {
	query := fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%s'",
		quoteMySQLIdentifier(dbName), m.escapeLiteral(userName), m.escapeLiteral(m.userHost()))
	_, err := m.db.ExecContext(ctx, query)
	return err
}

func (m *MySQLClient) flushPrivileges(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, "FLUSH PRIVILEGES")
	return err
}

// rollbackProvision drops whatever a failed provisioning run created. It uses
// a fresh context because the request context may already be cancelled.
func (m *MySQLClient) rollbackProvision(dbName, userName string, state *provisionState) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if state.userCreated {
		_, _ = m.db.ExecContext(cleanupCtx, fmt.Sprintf("DROP USER IF EXISTS '%s'@'%s'",
			m.escapeLiteral(userName), m.escapeLiteral(m.userHost())))
	}
	if state.databaseCreated {
		_, _ = m.db.ExecContext(cleanupCtx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", quoteMySQLIdentifier(dbName)))
	}
}

func (m *MySQLClient) databaseExists(ctx context.Context, name string) (bool, error) {
	var dbName string
	query := "SELECT SCHEMA_NAME FROM INFORMATION_SCHEMA.SCHEMATA WHERE SCHEMA_NAME = ?"
	err := m.db.QueryRowContext(ctx, query, name).Scan(&dbName)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Delete drops the user and database. Every step is checked: a partial
// deletion must surface, not silently succeed.
func (m *MySQLClient) Delete(ctx context.Context, databaseName, userName string) error {
	// Names come from Infisical, not the validated provision path — gate them
	// on the identifier allowlist before they reach any SQL.
	if err := validateIdentifier("database name", databaseName, maxIdentifierLen); err != nil {
		return err
	}
	if err := validateIdentifier("database user", userName, maxUserLen); err != nil {
		return err
	}

	if err := m.ensureConnection(ctx); err != nil {
		return err
	}

	if _, err := m.db.ExecContext(ctx, fmt.Sprintf("DROP USER IF EXISTS '%s'@'%s'",
		m.escapeLiteral(userName), m.escapeLiteral(m.userHost()))); err != nil {
		return fmt.Errorf("failed to drop user: %w", err)
	}
	if _, err := m.db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", quoteMySQLIdentifier(databaseName))); err != nil {
		return fmt.Errorf("failed to drop database: %w", err)
	}
	if _, err := m.db.ExecContext(ctx, "FLUSH PRIVILEGES"); err != nil {
		return fmt.Errorf("failed to flush privileges: %w", err)
	}
	return nil
}

// testMySQLAppConnection dials mysql with provisioned app credentials. The DSN
// is built through the driver's config builder so a password with special
// characters cannot corrupt it, and TLS follows the same secure default as the
// admin dial (overridable per engine config via sslMode).
func testMySQLAppConnection(ctx context.Context, creds Credentials, sslMode string) error {
	c := mysqldriver.NewConfig()
	c.User = creds.User
	c.Passwd = creds.Password
	c.Net = "tcp"
	c.Addr = net.JoinHostPort(creds.Host, creds.Port)
	c.DBName = creds.Name
	c.ParseTime = true
	c.Timeout = 5 * time.Second
	c.TLSConfig = mysqlTLSParam(sslMode)

	appDB, err := sql.Open("mysql", c.FormatDSN())
	if err != nil {
		return fmt.Errorf("failed to create connection: %w", err)
	}
	defer func() { _ = appDB.Close() }()

	if err := appDB.PingContext(ctx); err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	return nil
}
