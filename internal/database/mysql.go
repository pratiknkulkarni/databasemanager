package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

	// MySQL DSN format: user:password@tcp(host:port)/database
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
		m.cfg.MySQL.DatabaseUser,
		m.cfg.MySQL.DatabasePassword,
		m.cfg.MySQL.DatabaseHostname,
		m.cfg.MySQL.DatabasePort,
		m.cfg.MySQL.DatabaseName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to open mysql connection: %w", err)
	}

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

	state := &provisionStateMysql{}
	defer func() {
		if err != nil {
			m.rollbackProvision(ctx, opts.DatabaseName, opts.DatabaseUser, state)
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
	query := fmt.Sprintf("CREATE USER '%s'@'%%' IDENTIFIED BY '%s'", userName, password)
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

func (m *MySQLClient) rollbackProvision(ctx context.Context, dbName, userName string, state *provisionStateMysql) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if state.userCreated {
		m.db.ExecContext(cleanupCtx, fmt.Sprintf("DROP USER IF EXISTS '%s'@'%%'", userName))
	}
	if state.databaseCreated {
		m.db.ExecContext(cleanupCtx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
	}
}

func (m *MySQLClient) databaseExists(ctx context.Context, name string) (bool, error) {
	var dbName string
	query := "SELECT SCHEMA_NAME FROM INFORMATION_SCHEMA.SCHEMATA WHERE SCHEMA_NAME = ?"
	err := m.db.QueryRowContext(ctx, query, name).Scan(&dbName)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
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

	// Open admin connection
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?parseTime=true&timeout=10s",
		m.cfg.MySQL.DatabaseUser,
		m.cfg.MySQL.DatabasePassword,
		m.cfg.MySQL.DatabaseHostname,
		m.cfg.MySQL.DatabasePort)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	m.db = db
	return db.PingContext(ctx)
}

func (m *MySQLClient) Delete(ctx context.Context, databaseName, userName string) error {
	if err := m.ensureConnection(ctx); err != nil {
		return err
	}

	m.db.ExecContext(ctx, fmt.Sprintf("DROP USER IF EXISTS '%s'@'%%'", userName))
	m.db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", databaseName))
	m.db.ExecContext(ctx, "FLUSH PRIVILEGES")
	return nil
}

// TestAppConnection tests if the provisioned credentials actually work.
func (m *MySQLClient) TestAppConnection(ctx context.Context, credentials map[string]string) error {
	appDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&timeout=5s",
		credentials["DB_USER"],
		credentials["DB_PASSWORD"],
		credentials["DB_HOST"],
		credentials["DB_PORT"],
		credentials["DB_NAME"])

	appDB, err := sql.Open("mysql", appDSN)
	if err != nil {
		return fmt.Errorf("failed to create connection: %w", err)
	}
	defer appDB.Close()

	if err := appDB.PingContext(ctx); err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	return nil
}
