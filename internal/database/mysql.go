package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/praaatik/databasemanager/internal/config"
)

type MySQLClient struct {
	cfg config.DatabaseConfig
	db  *sql.DB
}

func newMySQLClient(cfg config.DatabaseConfig) *MySQLClient {
	return &MySQLClient{cfg: cfg}
}

// escapeMySQLLiteral escapes a value for interpolation into a single-quoted
// MySQL string literal. Backslashes must be escaped as well as quotes,
// otherwise a trailing backslash would escape the closing quote.
func escapeMySQLLiteral(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

// ensureConnection lazily opens the single admin connection (no database
// selected — provisioning and deletion operate across databases).
func (m *MySQLClient) ensureConnection(ctx context.Context) error {
	if m.db != nil {
		return nil
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?parseTime=true&timeout=10s",
		m.cfg.DatabaseUser,
		m.cfg.DatabasePassword,
		m.cfg.DatabaseHostname,
		m.cfg.DatabasePort)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to open mysql connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to ping mysql: %w", err)
	}

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

func (m *MySQLClient) createDatabase(ctx context.Context, dbName string) error {
	query := fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", dbName)
	_, err := m.db.ExecContext(ctx, query)
	return err
}

func (m *MySQLClient) createUser(ctx context.Context, userName, password string) error {
	query := fmt.Sprintf("CREATE USER '%s'@'%%' IDENTIFIED BY '%s'", userName, escapeMySQLLiteral(password))
	_, err := m.db.ExecContext(ctx, query)
	return err
}

func (m *MySQLClient) grantDatabasePrivileges(ctx context.Context, dbName, userName string) error {
	query := fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%'", dbName, userName)
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
		_, _ = m.db.ExecContext(cleanupCtx, fmt.Sprintf("DROP USER IF EXISTS '%s'@'%%'", userName))
	}
	if state.databaseCreated {
		_, _ = m.db.ExecContext(cleanupCtx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
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
	if err := m.ensureConnection(ctx); err != nil {
		return err
	}

	if _, err := m.db.ExecContext(ctx, fmt.Sprintf("DROP USER IF EXISTS '%s'@'%%'", escapeMySQLLiteral(userName))); err != nil {
		return fmt.Errorf("failed to drop user: %w", err)
	}
	if _, err := m.db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", strings.ReplaceAll(databaseName, "`", "``"))); err != nil {
		return fmt.Errorf("failed to drop database: %w", err)
	}
	if _, err := m.db.ExecContext(ctx, "FLUSH PRIVILEGES"); err != nil {
		return fmt.Errorf("failed to flush privileges: %w", err)
	}
	return nil
}

// testMySQLAppConnection dials mysql with provisioned app credentials.
func testMySQLAppConnection(ctx context.Context, creds Credentials) error {
	appDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&timeout=5s",
		creds.User,
		creds.Password,
		creds.Host,
		creds.Port,
		creds.Name)

	appDB, err := sql.Open("mysql", appDSN)
	if err != nil {
		return fmt.Errorf("failed to create connection: %w", err)
	}
	defer func() { _ = appDB.Close() }()

	if err := appDB.PingContext(ctx); err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	return nil
}
