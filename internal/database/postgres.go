package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/praaatik/databasemanager/internal/config"
)

const (
	DefaultOperationTimeout = 30 * time.Second
	DefaultPingTimeout      = 5 * time.Second
	DefaultMaxOpenConns     = 10
	DefaultMaxIdleConns     = 5
	DefaultConnMaxLifetime  = 5 * time.Minute
	DefaultConnMaxIdleTime  = 10 * time.Minute
)

// provisionState keeps a track of resources created
type provisionState struct {
	databaseCreated bool
	roleCreated     bool
}

type PostgresClient struct {
	cfg *config.Config
	db  *sql.DB
	mu  sync.RWMutex
}

func NewPostgresClient(cfg *config.Config) (*PostgresClient, error) {
	return &PostgresClient{cfg: cfg}, nil
}

func (p *PostgresClient) Connect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db != nil {
		return nil
	}

	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s sslmode=disable",
		p.cfg.DatabaseHostname, p.cfg.DatabasePort, p.cfg.DatabaseUser, p.cfg.DatabasePassword)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to open postgres connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping postgres: %w", err)
	}

	p.db = db
	return nil
}

func (p *PostgresClient) Test(ctx context.Context) error {
	p.mu.RLock()
	db := p.db
	p.mu.RUnlock()

	if db == nil {
		if err := p.Connect(ctx); err != nil {
			return fmt.Errorf("failed to establish connection: %w", err)
		}
		p.mu.RLock()
		db = p.db
		p.mu.RUnlock()
	}

	return db.PingContext(ctx)
}

func (p *PostgresClient) Provision(ctx context.Context, opts ProvisionOptions) (err error) {
	if err := opts.Validate(); err != nil {
		return fmt.Errorf("invalid provision options: %w", err)
	}

	databaseName := opts.Database
	if databaseName == "" {
		databaseName = fmt.Sprintf("%s_db", opts.AppName)
	}

	databaseUserName := opts.User
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

	if err := p.ensureConnection(ctx); err != nil {
		return err
	}

	exists, err := p.databaseExists(ctx, databaseName)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}
	if exists {
		return fmt.Errorf("database %q already exists", databaseName)
	}

	state := &provisionState{}
	defer func() {
		if err != nil {
			p.rollbackProvision(ctx, databaseName, databaseUserName, state)
		}
	}()

	appDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		p.cfg.DatabaseHostname,
		p.cfg.DatabasePort,
		p.cfg.DatabaseUser,
		p.cfg.DatabasePassword,
		databaseName)

	appDB, err := sql.Open("postgres", appDSN)
	if err != nil {
		return fmt.Errorf("open new database %q: %w", databaseName, err)
	}

	if err = p.createDatabase(ctx, appDB, databaseName); err != nil {
		return err
	}
	state.databaseCreated = true

	if err = p.createRole(ctx, databaseUserName, opts.AppPassword); err != nil {
		return err
	}
	state.roleCreated = true

	if err = p.alterDatabaseOwner(ctx, databaseName, databaseUserName); err != nil {
		return err
	}

	if err = p.configureNewDatabase(ctx, appDB, databaseName, databaseUserName, databaseSchemaName); err != nil {
		return err
	}

	return nil
}

func (p *PostgresClient) createDatabase(ctx context.Context, appDB *sql.DB, dbName string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	createDBSQL := fmt.Sprintf("CREATE DATABASE %s", pq.QuoteIdentifier(dbName))
	if _, err := p.db.ExecContext(ctxTimeout, createDBSQL); err != nil {
		return fmt.Errorf("failed to create database %q: %w", dbName, err)
	}
	return nil
}

// createRole creates a new database role with login and password
func (p *PostgresClient) createRole(ctx context.Context, userName, password string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	quotedUser := pq.QuoteIdentifier(userName)
	quotedPwd := pq.QuoteLiteral(password)
	createRoleSQL := fmt.Sprintf("CREATE ROLE %s WITH LOGIN PASSWORD %s", quotedUser, quotedPwd)

	if _, err := p.db.ExecContext(ctxTimeout, createRoleSQL); err != nil {
		return fmt.Errorf("failed to create role %q: %w", userName, err)
	}
	return nil
}

// alterDatabaseOwner changes the database owner
func (p *PostgresClient) alterDatabaseOwner(ctx context.Context, dbName, userName string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	alterOwnerSQL := fmt.Sprintf("ALTER DATABASE %s OWNER TO %s",
		pq.QuoteIdentifier(dbName), pq.QuoteIdentifier(userName))

	if _, err := p.db.ExecContext(ctxTimeout, alterOwnerSQL); err != nil {
		return fmt.Errorf("failed to change owner for database %q: %w", dbName, err)
	}
	return nil
}

// isolatePublicSchema restricts access to the public schema
func (p *PostgresClient) isolatePublicSchema(ctx context.Context, appDB *sql.DB, userName string) error {
	adminUser := pq.QuoteIdentifier(p.cfg.DatabaseUser)
	quotedUser := pq.QuoteIdentifier(userName)

	if _, err := appDB.ExecContext(ctx, fmt.Sprintf("ALTER SCHEMA public OWNER TO %s", adminUser)); err != nil {
		return fmt.Errorf("failed to change public schema owner: %w", err)
	}

	if _, err := appDB.ExecContext(ctx, fmt.Sprintf("REVOKE ALL ON SCHEMA public FROM %s", quotedUser)); err != nil {
		return fmt.Errorf("failed to revoke privileges on public schema from %q: %w", userName, err)
	}

	return nil
}

// configureNewDatabase connects to the newly created database and sets up schema
func (p *PostgresClient) configureNewDatabase(ctx context.Context, appDB *sql.DB, dbName, userName, schemaName string) error {
	var err error
	// appDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
	// 	p.cfg.DatabaseHostname,
	// 	p.cfg.DatabasePort,
	// 	p.cfg.DatabaseUser,
	// 	p.cfg.DatabasePassword,
	// 	dbName)

	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	if err = appDB.PingContext(ctxTimeout); err != nil {
		return fmt.Errorf("failed to ping new database %q: %w", dbName, err)
	}

	quotedUser := pq.QuoteIdentifier(userName)
	createSchemaSQL := fmt.Sprintf("CREATE SCHEMA %s AUTHORIZATION %s",
		pq.QuoteIdentifier(schemaName), quotedUser)
	if _, err = appDB.ExecContext(ctxTimeout, createSchemaSQL); err != nil {
		return fmt.Errorf("failed to create schema %q: %w", schemaName, err)
	}

	if err = p.isolatePublicSchema(ctx, appDB, userName); err != nil {
		return err
	}

	if err = p.revokePublicConnect(ctx, dbName); err != nil {
		return err
	}

	if err = p.setSearchPath(ctx, userName, schemaName); err != nil {
		return err
	}

	return nil
}

// revokePublicConnect revokes connect privilege on database from PUBLIC
func (p *PostgresClient) revokePublicConnect(ctx context.Context, dbName string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	revokeSQL := fmt.Sprintf("REVOKE CONNECT ON DATABASE %s FROM PUBLIC", pq.QuoteIdentifier(dbName))
	if _, err := p.db.ExecContext(ctxTimeout, revokeSQL); err != nil {
		return fmt.Errorf("failed to revoke CONNECT on %q: %w", dbName, err)
	}
	return nil
}

// setSearchPath sets the default search path for a role
func (p *PostgresClient) setSearchPath(ctx context.Context, userName, schemaName string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, DefaultOperationTimeout)
	defer cancel()

	setSearchPathSQL := fmt.Sprintf("ALTER ROLE %s SET search_path = %s",
		pq.QuoteIdentifier(userName), pq.QuoteIdentifier(schemaName))

	if _, err := p.db.ExecContext(ctxTimeout, setSearchPathSQL); err != nil {
		return fmt.Errorf("failed to set search_path for role %q: %w", userName, err)
	}
	return nil
}

// rollbackProvision attempts to clean up resources created during a failed provision
func (p *PostgresClient) rollbackProvision(ctx context.Context, dbName, userName string, state *provisionState) {
	// Use background context for cleanup to avoid cancelled context issues
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if state.databaseCreated {
		dropDBSQL := fmt.Sprintf("DROP DATABASE IF EXISTS %s", pq.QuoteIdentifier(dbName))
		if _, err := p.db.ExecContext(cleanupCtx, dropDBSQL); err != nil {
			// Log error but continue cleanup
			fmt.Fprintf(io.Discard, "rollback: failed to drop database %q: %v\n", dbName, err)
		}
	}

	if state.roleCreated {
		dropRoleSQL := fmt.Sprintf("DROP ROLE IF EXISTS %s", pq.QuoteIdentifier(userName))
		if _, err := p.db.ExecContext(cleanupCtx, dropRoleSQL); err != nil {
			fmt.Fprintf(io.Discard, "rollback: failed to drop role %q: %v\n", userName, err)
		}
	}
}

func validate(key string) (string, error) {
	// lowercase letter + max 63 characters
	var pattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return "", errors.New("identifier is empty")
	}
	if !pattern.MatchString(key) {
		return "", fmt.Errorf("invalid identifier %q: must match %s", key, pattern.String())
	}
	return key, nil
}

func (p *PostgresClient) databaseExists(ctx context.Context, name string) (bool, error) {
	var ok bool
	err := p.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&ok)
	return ok, err
}

func (p *PostgresClient) ensureConnection(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db != nil {
		return nil
	}

	if p.cfg == nil {
		return errors.New("config missing")
	}

	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s sslmode=disable",
		p.cfg.DatabaseHostname,
		p.cfg.DatabasePort,
		p.cfg.DatabaseUser,
		p.cfg.DatabasePassword)

	//TODO: hard coding this for now, check alternatives
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open admin connection: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping admin db: %w", err)
	}

	p.db = db
	return nil
}
