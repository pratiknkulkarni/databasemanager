package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	_ "github.com/go-sql-driver/mysql"
	"github.com/praaatik/databasemanager/internal/config"
)

// provisionStateMysql keeps track of resources created during provisioning
type provisionStateMysql struct {
	databaseCreated bool
	userCreated     bool
}

type MySQLClient struct {
	cfg *config.Config
	db  *sql.DB
	mu  sync.RWMutex
}

func NewMySQLClient(cfg *config.Config) (*MySQLClient, error) {
	return &MySQLClient{cfg: cfg}, nil
}

func (m *MySQLClient) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db != nil {
		return nil
	}

	fmt.Println(m.cfg.MySQL)

	// MySQL DSN format: user:password@tcp(host:port)/database
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		m.cfg.MySQL.DatabaseUser,
		m.cfg.MySQL.DatabasePassword,
		m.cfg.MySQL.DatabaseHostname,
		m.cfg.MySQL.DatabasePort,
		m.cfg.MySQL.DatabaseName)

	fmt.Println(dsn)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to open mysql connection: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(DefaultMaxOpenConns)
	db.SetMaxIdleConns(DefaultMaxIdleConns)
	db.SetConnMaxLifetime(DefaultConnMaxLifetime)
	db.SetConnMaxIdleTime(DefaultConnMaxIdleTime)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("failed to ping mysql: %w", err)
	}

	m.db = db
	return nil
}

func (m *MySQLClient) Test(ctx context.Context) error {
	m.mu.RLock()
	db := m.db
	m.mu.RUnlock()

	if db == nil {
		if err := m.Connect(ctx); err != nil {
			return fmt.Errorf("failed to establish connection: %w", err)
		}
		m.mu.RLock()
		db = m.db
		m.mu.RUnlock()
	}

	return db.PingContext(ctx)
}

func (m *MySQLClient) Provision(ctx context.Context, opts ProvisionOptions) (err error) {
	if err := opts.Validate(); err != nil {
		return fmt.Errorf("invalid provision options: %w", err)
	}

	databaseName := opts.DatabaseName
	if databaseName == "" {
		databaseName = fmt.Sprintf("%s_db", opts.AppName)
	}

	databaseUserName := opts.DatabaseUser
	if databaseUserName == "" {
		databaseUserName = fmt.Sprintf("%s_user", opts.AppName)
	}

	databaseSchemaName := opts.Schema
	if databaseSchemaName == "" {
		databaseSchemaName = fmt.Sprintf("%s_data", opts.AppName)
	}

	databaseName, err = validate(databaseName)
	if err != nil {
		return fmt.Errorf("invalid database name: %w", err)
	}

	databaseUserName, err = validate(databaseUserName)
	if err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	databaseSchemaName, err = validate(databaseSchemaName)
	if err != nil {
		return fmt.Errorf("invalid schema name: %w", err)
	}

	if err := m.ensureConnection(ctx); err != nil {
		return err
	}

	exists, err := m.databaseExists(ctx, databaseName)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}
	if exists {
		return fmt.Errorf("database %q already exists", databaseName)
	}

	state := &provisionStateMysql{}
	defer func() {
		if err != nil {
			m.rollbackProvision(ctx, databaseName, databaseUserName, state)
		}
	}()

	// Create database
	if err = m.createDatabase(ctx, databaseName); err != nil {
		return err
	}
	state.databaseCreated = true

	// Create user
	if err = m.createUser(ctx, databaseUserName, opts.DatabasePassword); err != nil {
		return err
	}
	state.userCreated = true

	// Grant all privileges on the database to the user
	if err = m.grantDatabasePrivileges(ctx, databaseName, databaseUserName); err != nil {
		return err
	}

	// Flush privileges to ensure they take effect
	if err = m.flushPrivileges(ctx); err != nil {
		return err
	}

	return nil
}

func (m *MySQLClient) createDatabase(ctx context.Context, dbName string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	createDBSQL := fmt.Sprintf("CREATE DATABASE %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		quoteIdentifier(dbName))

	if _, err := m.db.ExecContext(ctxTimeout, createDBSQL); err != nil {
		return fmt.Errorf("failed to create database %q: %w", dbName, err)
	}
	return nil
}

// createUser creates a new MySQL user with password
func (m *MySQLClient) createUser(ctx context.Context, userName, password string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	createUserSQL := fmt.Sprintf("CREATE USER %s@'%%' IDENTIFIED BY %s",
		quoteIdentifier(userName),
		quoteLiteral(password))

	if _, err := m.db.ExecContext(ctxTimeout, createUserSQL); err != nil {
		fmt.Println("create user failed", "sql", createUserSQL, "err", err)
		return fmt.Errorf("failed to create user %q: %w", userName, err)
	}

	return nil
}

// grantDatabasePrivileges grants all privileges on a database to a user
func (m *MySQLClient) grantDatabasePrivileges(ctx context.Context, dbName, userName string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	grantSQL := fmt.Sprintf("GRANT ALL PRIVILEGES ON %s.* TO %s@'%%'",
		quoteIdentifier(dbName),
		quoteIdentifier(userName))

	if _, err := m.db.ExecContext(ctxTimeout, grantSQL); err != nil {
		return fmt.Errorf("failed to grant privileges on database %q to user %q: %w", dbName, userName, err)
	}

	return nil
}

// flushPrivileges reloads the grant tables
func (m *MySQLClient) flushPrivileges(ctx context.Context) error {
	return nil
}

// rollbackProvision attempts to clean up resources created during a failed provision
func (m *MySQLClient) rollbackProvision(ctx context.Context, dbName, userName string, state *provisionStateMysql) {
}

func (m *MySQLClient) databaseExists(ctx context.Context, name string) (bool, error) {
	return false, nil
}

func (m *MySQLClient) userExists(ctx context.Context, userName string) (bool, error) {
	return false, nil
}

func (m *MySQLClient) ensureConnection(ctx context.Context) error {
	return nil
}

func (m *MySQLClient) Delete(ctx context.Context, databaseName, userName string) error {
	return nil
}

// terminateConnections kills all connections to a specific database
func (m *MySQLClient) terminateConnections(ctx context.Context, databaseName string) error {
	return nil
}

// TestAppConnection tests a database connection using app credentials
func (m *MySQLClient) TestAppConnection(ctx context.Context, credentials map[string]string) error {
	return nil
}

func (m *MySQLClient) Debug() {
	fmt.Println(m.cfg)
}

// quoteIdentifier quotes a MySQL identifier (database, table, column names)
// MySQL uses backticks for quoting identifiers
func quoteIdentifier(name string) string {
	// Escape any backticks in the name
	escaped := strings.ReplaceAll(name, "`", "``")
	return fmt.Sprintf("`%s`", escaped)
}

// quoteLiteral quotes a string literal for MySQL
func quoteLiteral(s string) string {
	// Escape single quotes and backslashes
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	return fmt.Sprintf("'%s'", escaped)
}
