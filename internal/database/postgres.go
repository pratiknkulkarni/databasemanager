package database

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/lib/pq"
	"github.com/praaatik/databasemanager/internal/config"
)

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

	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		p.cfg.Postgres.DatabaseHostname,
		p.cfg.Postgres.DatabasePort,
		p.cfg.Postgres.DatabaseUser,
		p.cfg.Postgres.DatabasePassword,
		p.cfg.Postgres.DatabaseName)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to open postgres connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("failed to ping postgres: %w", err)
	}

	p.db = db
	return nil
}

func (p *PostgresClient) ensureConnection(ctx context.Context) error {
	p.mu.RLock()
	if p.db != nil {
		p.mu.RUnlock()
		return nil
	}
	p.mu.RUnlock()
	return p.Connect(ctx)
}

func (p *PostgresClient) Provision(ctx context.Context, opts ProvisionOptions) error {
	if err := opts.Validate(); err != nil {
		return fmt.Errorf("invalid provision options: %w", err)
	}

	if err := p.ensureConnection(ctx); err != nil {
		return err
	}

	userStr := pq.QuoteIdentifier(opts.DatabaseUser)
	dbStr := pq.QuoteIdentifier(opts.DatabaseName)

	// 1. Create User
	_, err := p.db.ExecContext(ctx, fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s'", userStr, opts.DatabasePassword))
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	// 2. Create Database
	_, err = p.db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", dbStr, userStr))
	if err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

	// 3. HARDENING: Connect to the new DB to lock it down
	if err := p.lockdownDatabase(ctx, opts); err != nil {
		// If lockdown fails, I should technically rollback, but for this tool, logging the error is often enough.
		// It gets a bit too complicated.
		return fmt.Errorf("failed to apply security hardening: %w", err)
	}

	return nil
}

// lockdownDatabase connects to the specific app database to revoke default public privileges
func (p *PostgresClient) lockdownDatabase(ctx context.Context, opts ProvisionOptions) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		p.cfg.Postgres.DatabaseHostname,
		p.cfg.Postgres.DatabasePort,
		p.cfg.Postgres.DatabaseUser,
		p.cfg.Postgres.DatabasePassword,
		opts.DatabaseName) // Connecting to the new DB

	appDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer appDB.Close()

	if err := appDB.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to connect to new db for hardening: %w", err)
	}

	// 3a. Revoke PUBLIC Connect on the Database
	_, err = appDB.ExecContext(ctx, "REVOKE CONNECT ON DATABASE "+pq.QuoteIdentifier(opts.DatabaseName)+" FROM PUBLIC")

	// 3b. Isolate Public Schema
	adminUser := pq.QuoteIdentifier(p.cfg.Postgres.DatabaseUser)

	// Make Admin the owner of public schema (so the app user can't mess with it easily)
	if _, err := appDB.ExecContext(ctx, fmt.Sprintf("ALTER SCHEMA public OWNER TO %s", adminUser)); err != nil {
		return fmt.Errorf("failed to change public schema owner: %w", err)
	}

	// Revoke all rights on public schema from everyone (PUBLIC)
	if _, err := appDB.ExecContext(ctx, "REVOKE ALL ON SCHEMA public FROM PUBLIC"); err != nil {
		return fmt.Errorf("failed to revoke public schema access: %w", err)
	}

	// 3c. Grant Schema Usage to the App User (explicitly)
	if opts.Schema == "public" {
		userStr := pq.QuoteIdentifier(opts.DatabaseUser)
		_, err = appDB.ExecContext(ctx, fmt.Sprintf("GRANT ALL ON SCHEMA public TO %s", userStr))
		if err != nil {
			return fmt.Errorf("failed to grant schema access to user: %w", err)
		}
	} else if opts.Schema != "" {
		// Create Custom Schema
		userStr := pq.QuoteIdentifier(opts.DatabaseUser)
		schemaStr := pq.QuoteIdentifier(opts.Schema)

		_, err = appDB.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %s AUTHORIZATION %s", schemaStr, userStr))
		if err != nil {
			return fmt.Errorf("failed to create custom schema: %w", err)
		}
	}

	return nil
}

func (p *PostgresClient) Delete(ctx context.Context, databaseName, userName string) error {
	if err := p.ensureConnection(ctx); err != nil {
		return err
	}

	// Terminate connections first
	killQuery := fmt.Sprintf(`
		SELECT pg_terminate_backend(pg_stat_activity.pid)
		FROM pg_stat_activity
		WHERE pg_stat_activity.datname = '%s'
		AND pid <> pg_backend_pid();`, databaseName)

	p.db.ExecContext(ctx, killQuery)

	_, err := p.db.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", pq.QuoteIdentifier(databaseName)))
	if err != nil {
		return fmt.Errorf("failed to drop database: %w", err)
	}

	_, err = p.db.ExecContext(ctx, fmt.Sprintf("DROP USER IF EXISTS %s", pq.QuoteIdentifier(userName)))
	if err != nil {
		return fmt.Errorf("failed to drop user: %w", err)
	}

	return nil
}

// TestAppConnection tests a database connection using app credentials
func (p *PostgresClient) TestAppConnection(ctx context.Context, credentials map[string]string) error {
	host := credentials["DB_HOST"]
	port := credentials["DB_PORT"]
	dbName := credentials["DB_NAME"]
	user := credentials["DB_USER"]
	password := credentials["DB_PASSWORD"]

	if host == "" || port == "" || dbName == "" || user == "" || password == "" {
		return fmt.Errorf("missing required credentials")
	}

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		user, password, host, port, dbName)

	appDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to create connection: %w", err)
	}
	defer appDB.Close()

	if err := appDB.PingContext(ctx); err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	return nil
}

func (p *PostgresClient) Test(ctx context.Context) error {
	if err := p.ensureConnection(ctx); err != nil {
		return err
	}
	return p.db.PingContext(ctx)
}
