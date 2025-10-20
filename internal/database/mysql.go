package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

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

	databaseName, err = validate(databaseName)
	if err != nil {
		return fmt.Errorf("invalid database name: %w", err)
	}

	databaseUserName, err = validate(databaseUserName)
	if err != nil {
		return fmt.Errorf("invalid username: %w", err)
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

// createDatabase creates a new MySQL database
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
	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	if _, err := m.db.ExecContext(ctxTimeout, "FLUSH PRIVILEGES"); err != nil {
		return fmt.Errorf("failed to flush privileges: %w", err)
	}
	return nil
}

// rollbackProvision attempts to clean up resources created during a failed provision
func (m *MySQLClient) rollbackProvision(ctx context.Context, dbName, userName string, state *provisionStateMysql) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if state.userCreated {
		dropUserSQL := fmt.Sprintf("DROP USER IF EXISTS %s@'%%'", quoteIdentifier(userName))
		if _, err := m.db.ExecContext(cleanupCtx, dropUserSQL); err != nil {
			slog.Warn("rollback: failed to drop user", "user", userName, "error", err)
		}
	}

	if state.databaseCreated {
		dropDBSQL := fmt.Sprintf("DROP DATABASE IF EXISTS %s", quoteIdentifier(dbName))
		if _, err := m.db.ExecContext(cleanupCtx, dropDBSQL); err != nil {
			slog.Warn("rollback: failed to drop database", "db", dbName, "error", err)
		}
	}
}

// databaseExists checks whether the database with the "name" is already present in MySQL
func (m *MySQLClient) databaseExists(ctx context.Context, name string) (bool, error) {
	var dbName string
	query := "SELECT SCHEMA_NAME FROM INFORMATION_SCHEMA.SCHEMATA WHERE SCHEMA_NAME = ?"
	err := m.db.QueryRowContext(ctx, query, name).Scan(&dbName)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (m *MySQLClient) userExists(ctx context.Context, userName string) (bool, error) {
	var user string
	query := "SELECT DatabaseUser FROM mysql.user WHERE DatabaseUser = ? AND Host = '%'"
	err := m.db.QueryRowContext(ctx, query, userName).Scan(&user)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (m *MySQLClient) ensureConnection(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db != nil {
		return nil
	}

	if m.cfg == nil {
		return errors.New("config missing")
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?parseTime=true&timeout=10s",
		m.cfg.MySQL.DatabaseUser,
		m.cfg.MySQL.DatabasePassword,
		m.cfg.MySQL.DatabaseHostname,
		m.cfg.MySQL.DatabasePort)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open admin connection: %w", err)
	}

	db.SetMaxOpenConns(DefaultMaxOpenConns)
	db.SetMaxIdleConns(DefaultMaxIdleConns)
	db.SetConnMaxLifetime(DefaultConnMaxLifetime)
	db.SetConnMaxIdleTime(DefaultConnMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, DefaultPingTimeout)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping admin db: %w", err)
	}

	m.db = db
	return nil
}
func (m *MySQLClient) Delete(ctx context.Context, databaseName, userName string) error {
	userName, err := validate(userName)
	if err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	databaseName, err = validate(databaseName)
	if err != nil {
		return fmt.Errorf("invalid database name: %w", err)
	}

	if err := m.ensureConnection(ctx); err != nil {
		return err
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	exists, err := m.userExists(ctx, userName)
	if err != nil {
		return fmt.Errorf("failed to check if user exists: %w", err)
	}

	if !exists {
		return fmt.Errorf("user %q does not exist", userName)
	}

	dbExists, err := m.databaseExists(ctx, databaseName)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}

	if !dbExists {
		return fmt.Errorf("database %q does not exist", databaseName)
	}

	if err := m.terminateConnections(ctx, databaseName); err != nil {
		// Log warning but continue
		fmt.Fprintf(os.Stderr, "warning: failed to terminate connections for database %q: %v\n", databaseName, err)
	}

	revokeSQL := fmt.Sprintf("REVOKE ALL PRIVILEGES, GRANT OPTION FROM %s@'%%'",
		quoteIdentifier(userName))
	if _, err := m.db.ExecContext(ctxTimeout, revokeSQL); err != nil {
		// Log warning but continue
		fmt.Fprintf(os.Stderr, "warning: failed to revoke privileges for user %q: %v\n", userName, err)
	}

	dropUserSQL := fmt.Sprintf("DROP USER IF EXISTS %s@'%%'", quoteIdentifier(userName))
	if _, err := m.db.ExecContext(ctxTimeout, dropUserSQL); err != nil {
		return fmt.Errorf("failed to drop user %q: %w", userName, err)
	}

	dropDBSQL := fmt.Sprintf("DROP DATABASE IF EXISTS %s", quoteIdentifier(databaseName))
	if _, err := m.db.ExecContext(ctxTimeout, dropDBSQL); err != nil {
		return fmt.Errorf("failed to drop database %q: %w", databaseName, err)
	}

	if err := m.flushPrivileges(ctx); err != nil {
		// Log warning but don't fail - cleanup was successful
		fmt.Fprintf(os.Stderr, "warning: failed to flush privileges: %v\n", err)
	}

	return nil
}

// terminateConnections kills all connections to a specific database
func (m *MySQLClient) terminateConnections(ctx context.Context, databaseName string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	query := "SELECT ID FROM INFORMATION_SCHEMA.PROCESSLIST WHERE DB = ?"
	rows, err := m.db.QueryContext(ctxTimeout, query, databaseName)
	if err != nil {
		return fmt.Errorf("failed to query processlist: %w", err)
	}
	defer rows.Close()

	var processIDs []int64
	for rows.Next() {
		var pid int64
		if err := rows.Scan(&pid); err != nil {
			return fmt.Errorf("failed to scan process ID: %w", err)
		}
		processIDs = append(processIDs, pid)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating processlist: %w", err)
	}

	for _, pid := range processIDs {
		killSQL := fmt.Sprintf("KILL %d", pid)
		if _, err := m.db.ExecContext(ctxTimeout, killSQL); err != nil {
			// continue since onnection might have already have been closed
			fmt.Fprintf(os.Stderr, "warning: failed to kill connection %d: %v\n", pid, err)
		}
	}

	return nil
}

// TestAppConnection tests whether the credentials created for the application are working.
func (m *MySQLClient) TestAppConnection(ctx context.Context, credentials map[string]string) error {
	host := credentials["DB_HOST"]
	port := credentials["DB_PORT"]
	dbName := credentials["DB_NAME"]
	user := credentials["DB_USER"]
	password := credentials["DB_PASSWORD"]

	// Check ALL secrets before testing
	missingFields := []string{}
	if host == "" {
		missingFields = append(missingFields, "DB_HOST")
	}
	if port == "" {
		missingFields = append(missingFields, "DB_PORT")
	}
	if dbName == "" {
		missingFields = append(missingFields, "DB_NAME")
	}
	if user == "" {
		missingFields = append(missingFields, "DB_USER")
	}
	if password == "" {
		missingFields = append(missingFields, "DB_PASSWORD")
	}

	if len(missingFields) > 0 {
		return fmt.Errorf("missing required credentials: %v", missingFields)
	}

	// MySQL DSN format: user:password@tcp(host:port)/database
	appDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s",
		user,
		password+" root",
		host,
		port,
		dbName)
	fmt.Println(appDSN)

	appDB, err := sql.Open("mysql", appDSN)
	if err != nil {
		return fmt.Errorf("failed to create connection: %w", err)
	}
	defer appDB.Close()

	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultPingTimeout)
	defer cancel()

	if err := appDB.PingContext(ctxTimeout); err != nil {
		return fmt.Errorf("failed to connect to database %q as user %q: %w", dbName, user, err)
	}

	queryCtx, cancel := context.WithTimeout(ctx, DefaultPingTimeout)
	defer cancel()

	var dbVersion string
	if err := appDB.QueryRowContext(queryCtx, "SELECT VERSION()").Scan(&dbVersion); err != nil {
		return fmt.Errorf("connected but failed to query database: %w", err)
	}

	return nil
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
