package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/praaatik/databasemanager/internal/config"
)

type PostgresClient struct {
	cfg *config.Config
	db  *sql.DB
	mu  sync.Mutex
}

func NewPostgresClient(cfg *config.Config) (*PostgresClient, error) {
	return &PostgresClient{cfg: cfg}, nil
}

func (p *PostgresClient) Connect(ctx context.Context) error {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s sslmode=disable",
		p.cfg.DatabaseHostname, p.cfg.DatabasePort, p.cfg.DatabaseUser, p.cfg.DatabasePassword)

	fmt.Println(p.cfg.DatabaseHostname, p.cfg.DatabasePort, p.cfg.DatabaseUser, p.cfg.DatabasePassword)

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
	if p.db == nil {
		if err := p.Connect(ctx); err != nil {
			return err
		}
	}
	return p.db.PingContext(ctx)
}

func (p *PostgresClient) Provision(ctx context.Context, appName, appPassword string) (err error) {
	if strings.TrimSpace(appName) == "" {
		return errors.New("appName must not be empty")
	}
	if len(appPassword) == 0 {
		return errors.New("appPassword must not be empty")
	}

	if err = p.ensureConnection(ctx); err != nil {
		return err
	}

	dbNameBase := fmt.Sprintf("%s_db", appName)
	userNameBase := fmt.Sprintf("%s_user", appName)
	schemaNameBase := fmt.Sprintf("%s_data", appName)

	dbName, err := validate(dbNameBase)
	if err != nil {
		return fmt.Errorf("invalid database name: %w", err)
	}
	userName, err := validate(userNameBase)
	if err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}
	schemaName, err := validate(schemaNameBase)
	if err != nil {
		return fmt.Errorf("invalid schema name: %w", err)
	}

	exists, err := p.databaseExists(ctx, dbName)
	if err != nil {
		return fmt.Errorf("check database exists: %w", err)
	}
	if exists {
		return fmt.Errorf("database %q already exists", dbName)
	}

	dbCreated := false
	defer func() {
		// rollback database and role creations
		if err != nil && dbCreated {
			_, _ = p.db.ExecContext(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, pq.QuoteIdentifier(dbName)))
			_, _ = p.db.ExecContext(ctx, fmt.Sprintf(`DROP ROLE IF EXISTS %s`, pq.QuoteIdentifier(userName)))
		}
	}()

	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// create database
	createDBSQL := fmt.Sprintf("CREATE DATABASE %s", pq.QuoteIdentifier(dbName))
	if _, err = p.db.ExecContext(ctxTimeout, createDBSQL); err != nil {
		return fmt.Errorf("create database %q: %w", dbName, err)
	}
	dbCreated = true

	// create role
	quotedUser := pq.QuoteIdentifier(userName)
	quotedPwd := pq.QuoteLiteral(appPassword)
	createRoleSQL := fmt.Sprintf("CREATE ROLE %s WITH LOGIN PASSWORD %s", quotedUser, quotedPwd)
	if _, err = p.db.ExecContext(ctxTimeout, createRoleSQL); err != nil {
		return fmt.Errorf("create role %q: %w", userName, err)
	}

	// transfer db ownership
	alterOwnerSQL := fmt.Sprintf("ALTER DATABASE %s OWNER TO %s", pq.QuoteIdentifier(dbName), quotedUser)
	if _, err = p.db.ExecContext(ctxTimeout, alterOwnerSQL); err != nil {
		return fmt.Errorf("assign owner for database %q: %w", dbName, err)
	}

	// connect to the new database
	appDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		p.cfg.DatabaseHostname,
		p.cfg.DatabasePort,
		p.cfg.DatabaseUser,
		p.cfg.DatabasePassword,
		dbName)

	appDB, err := sql.Open("postgres", appDSN)
	if err != nil {
		return fmt.Errorf("open new database %q: %w", dbName, err)
	}

	defer appDB.Close()

	if err = appDB.PingContext(ctxTimeout); err != nil {
		return fmt.Errorf("ping new database %q: %w", dbName, err)
	}

	appDB.SetMaxOpenConns(5)
	appDB.SetMaxIdleConns(2)
	appDB.SetConnMaxLifetime(5 * time.Minute)

	// create schema
	createSchemaSQL := fmt.Sprintf("CREATE SCHEMA %s AUTHORIZATION %s",
		pq.QuoteIdentifier(schemaName), quotedUser)
	if _, err = appDB.ExecContext(ctxTimeout, createSchemaSQL); err != nil {
		return fmt.Errorf("create schema %q: %w", schemaName, err)
	}

	// isolate "public"
	adminUser := pq.QuoteIdentifier(p.cfg.DatabaseUser)

	// change ownership of public to current admin
	if _, err = appDB.ExecContext(ctx, fmt.Sprintf("ALTER SCHEMA public OWNER TO %s", adminUser)); err != nil {
		return fmt.Errorf("change public schema owner: %w", err)
	}

	// revoke privileges from app user
	if _, err = appDB.ExecContext(ctx, fmt.Sprintf("REVOKE ALL ON SCHEMA public FROM %s", quotedUser)); err != nil {
		return fmt.Errorf("revoke privileges on public schema from %q: %w", userName, err)
	}

	// revoke CONNECTion for other users
	if _, err = p.db.ExecContext(ctxTimeout, fmt.Sprintf("REVOKE CONNECT ON DATABASE %s FROM PUBLIC", pq.QuoteIdentifier(dbName))); err != nil {
		return fmt.Errorf("revoke CONNECT on %q: %w", dbName, err)
	}

	setSearchPathSQL := fmt.Sprintf("ALTER ROLE %s SET search_path = %s", quotedUser, pq.QuoteIdentifier(schemaName))
	if _, err = p.db.ExecContext(ctxTimeout, setSearchPathSQL); err != nil {
		return fmt.Errorf("set search_path for role %q: %w", userName, err)
	}

	return nil
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
