package services

import (
	"context"
	"io"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
)

// --- Mocks ---

// MockDB implements database.Database for testing purposes
type MockDB struct {
	ProvisionCalled bool
	ProvisionOpts   database.ProvisionOptions
	DeleteCalled    bool
	DeletedName     string
	DeletedUser     string
}

func (m *MockDB) Provision(_ context.Context, opts database.ProvisionOptions) error {
	m.ProvisionCalled = true
	m.ProvisionOpts = opts
	return nil
}

func (m *MockDB) Delete(_ context.Context, databaseName, userName string) error {
	m.DeleteCalled = true
	m.DeletedName = databaseName
	m.DeletedUser = userName
	return nil
}

func (m *MockDB) Close() error {
	return nil
}

func newTestProvisioner(mockDB *MockDB, engine string, engineCfg config.DatabaseConfig) *Provisioner {
	return NewProvisioner(ProvisionerDeps{
		DB:        mockDB,
		Secrets:   nil, // Intentionally nil: provisioning must not panic offline
		Logger:    log.New(io.Discard),
		ProjectID: "test-project",
		Engine:    engine,
		EngineCfg: engineCfg,
	})
}

// --- Tests ---

func TestProvisioner_Run_Defaults(t *testing.T) {
	mockDB := &MockDB{}
	engineCfg := config.DatabaseConfig{
		DatabaseHostname: "localhost",
		DatabasePort:     5432,
	}

	provisioner := newTestProvisioner(mockDB, database.EnginePostgres, engineCfg)

	req := ProvisionRequest{
		AppName: "test-service",
	}

	result, err := provisioner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !mockDB.ProvisionCalled {
		t.Error("expected DB.Provision to be called")
	}

	// Both derived names must be sanitized: the hyphen in the app name maps
	// to an underscore in the database name AND the user name.
	if got, want := result.Options.DatabaseName, "test_service_db"; got != want {
		t.Errorf("expected db name %q, got %q", want, got)
	}
	if got, want := result.Options.DatabaseUser, "test_service_user"; got != want {
		t.Errorf("expected db user %q, got %q", want, got)
	}

	if result.Options.Schema != "public" {
		t.Errorf("expected default schema 'public', got %q", result.Options.Schema)
	}

	if len(result.Options.DatabasePassword) == 0 {
		t.Error("expected password to be generated")
	}

	// With a nil Infisical client, the result must not claim the secrets
	// were synced.
	if result.Synced {
		t.Error("expected Synced=false when Infisical client is nil")
	}

	// The credentials mirror the provisioned options.
	if result.Credentials.Password != result.Options.DatabasePassword {
		t.Error("expected result credentials to carry the generated password")
	}
	if result.Credentials.Type != database.EnginePostgres {
		t.Errorf("expected credentials type 'postgres', got %q", result.Credentials.Type)
	}
}

func TestProvisioner_Run_RecordsEngineConfigFacts(t *testing.T) {
	mockDB := &MockDB{}
	// Non-default port: the recorded connection facts must come from the
	// engine configuration the adapter dials with, not hardcoded defaults.
	engineCfg := config.DatabaseConfig{
		DatabaseHostname: "db.internal",
		DatabasePort:     5433,
	}

	provisioner := newTestProvisioner(mockDB, database.EnginePostgres, engineCfg)

	result, err := provisioner.Run(context.Background(), ProvisionRequest{AppName: "myapp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := result.Options.DatabaseHostname, "db.internal"; got != want {
		t.Errorf("expected recorded host %q, got %q", want, got)
	}
	if got, want := result.Options.DatabasePort, 5433; got != want {
		t.Errorf("expected recorded port %d, got %d", want, got)
	}
	if got, want := result.Credentials.Port, "5433"; got != want {
		t.Errorf("expected credentials port %q, got %q", want, got)
	}
}

func TestProvisioner_Run_Overrides(t *testing.T) {
	mockDB := &MockDB{}
	engineCfg := config.DatabaseConfig{
		DatabaseHostname: "127.0.0.1",
		DatabasePort:     3306,
	}

	provisioner := newTestProvisioner(mockDB, database.EngineMySQL, engineCfg)

	req := ProvisionRequest{
		AppName:    "legacy-app",
		DBName:     "custom_db_name",
		DBUser:     "custom_user",
		DBPassword: "StaticPassword123!",
		Host:       "override.host",
		Port:       3307,
	}

	result, err := provisioner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mockDB.ProvisionOpts.DatabaseName != "custom_db_name" {
		t.Errorf("expected db name override to 'custom_db_name', got %q", mockDB.ProvisionOpts.DatabaseName)
	}
	if mockDB.ProvisionOpts.DatabaseUser != "custom_user" {
		t.Errorf("expected db user override to 'custom_user', got %q", mockDB.ProvisionOpts.DatabaseUser)
	}
	if mockDB.ProvisionOpts.DatabasePassword != "StaticPassword123!" {
		t.Errorf("expected password override to work")
	}
	if mockDB.ProvisionOpts.DatabaseHostname != "override.host" {
		t.Errorf("expected host override to 'override.host', got %q", mockDB.ProvisionOpts.DatabaseHostname)
	}
	if mockDB.ProvisionOpts.DatabasePort != 3307 {
		t.Errorf("expected port override to 3307, got %d", mockDB.ProvisionOpts.DatabasePort)
	}

	if result.Options.DatabaseName != "custom_db_name" {
		t.Errorf("result object mismatch")
	}

	// MySQL apps must not carry a schema.
	if result.Options.Schema != "" {
		t.Errorf("expected empty schema for mysql, got %q", result.Options.Schema)
	}
}

func TestProvisioner_Run_RejectsSchemaForMySQL(t *testing.T) {
	mockDB := &MockDB{}
	provisioner := newTestProvisioner(mockDB, database.EngineMySQL, config.DatabaseConfig{
		DatabaseHostname: "127.0.0.1",
		DatabasePort:     3306,
	})

	_, err := provisioner.Run(context.Background(), ProvisionRequest{
		AppName:  "myapp",
		DBSchema: "not_allowed",
	})
	if err == nil {
		t.Fatal("expected error when passing a schema for mysql")
	}
	if mockDB.ProvisionCalled {
		t.Error("expected DB.Provision NOT to be called when the request is invalid")
	}
}

func TestSanitizeAppName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"with-hyphen", "with_hyphen"},
		{"multi-part-name", "multi_part_name"},
	}
	for _, tt := range tests {
		if got := sanitizeAppName(tt.in); got != tt.want {
			t.Errorf("sanitizeAppName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
