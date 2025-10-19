package database

import (
	"context"
	"database/sql"
	"fmt"
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

	// MySQL DSN format: user:password@tcp(host:port)/
	//dsn := "primary_user:root@tcp(localhost:3306)"
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

	fmt.Println("test working")
	return db.PingContext(ctx)
}

func (m *MySQLClient) Provision(ctx context.Context, opts ProvisionOptions) (err error) {
	fmt.Println("we'll provision the mysql")
	return nil
}

func (m *MySQLClient) createDatabase(ctx context.Context, dbName string) error {
	return nil
}

// createUser creates a new MySQL user with password
func (m *MySQLClient) createUser(ctx context.Context, userName, password string) error {
	return nil
}

// grantDatabasePrivileges grants all privileges on a database to a user
func (m *MySQLClient) grantDatabasePrivileges(ctx context.Context, dbName, userName string) error {
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
