package services

import (
	"context"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
)

// --- Mocks ---

// MockDB implements database.Database for testing purposes
type MockDB struct {
	ProvisionCalled bool
	ProvisionOpts   database.ProvisionOptions
}

func (m *MockDB) Connect(_ context.Context) error {
	//TODO implement me
	panic("implement me")
}

func (m *MockDB) Provision(_ context.Context, opts database.ProvisionOptions) error {
	m.ProvisionCalled = true
	m.ProvisionOpts = opts
	return nil
}

func (m *MockDB) Delete(_ context.Context, _, _ string) error {
	return nil
}

func (m *MockDB) Test(_ context.Context) error {
	return nil
}

func (m *MockDB) TestAppConnection(_ context.Context, _ map[string]string) error {
	return nil
}

// --- Tests ---

func TestProvisioner_Run_Defaults(t *testing.T) {
	// 1. Setup
	mockDB := &MockDB{}
	cfg := &config.Config{
		Postgres: config.DatabaseConfig{
			DatabaseHostname: "localhost",
			DatabasePort:     5432,
		},
	}

	// We inject the MockDB into the container
	container := &app.Container{
		Config:    cfg,
		Logger:    log.New(nil), // Silent logger
		DB:        mockDB,
		Infisical: nil, // Intentionally nil to verify it doesn't panic
	}

	provisioner := NewProvisioner(container)

	// 2. Execution
	req := ProvisionRequest{
		AppName: "test-service",
		Type:    "postgres",
	}

	result, err := provisioner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// 3. Assertions

	// Did it call the DB?
	if !mockDB.ProvisionCalled {
		t.Error("expected DB.Provision to be called")
	}

	// Did it generate the right names?
	expectedDB := "test_service_db"
	if result.DatabaseName != expectedDB {
		t.Errorf("expected db name '%s', got '%s'", expectedDB, result.DatabaseName)
	}

	// Did it default to public schema for Postgres?
	if result.Schema != "public" {
		t.Errorf("expected default schema 'public', got '%s'", result.Schema)
	}

	// Did it generate a password?
	if len(result.DatabasePassword) == 0 {
		t.Error("expected password to be generated")
	}
}

func TestProvisioner_Run_Overrides(t *testing.T) {
	mockDB := &MockDB{}
	cfg := &config.Config{
		MySQL: config.DatabaseConfig{
			DatabaseHostname: "127.0.0.1",
			DatabasePort:     3306,
		},
	}

	container := &app.Container{
		Config: cfg,
		Logger: log.New(nil),
		DB:     mockDB,
	}

	provisioner := NewProvisioner(container)

	req := ProvisionRequest{
		AppName:    "legacy-app",
		Type:       "mysql",
		DBName:     "custom_db_name",
		DBUser:     "custom_user",
		DBPassword: "StaticPassword123!",
		DBSchema:   "ignored_for_mysql", // Should be ignored or handled
	}

	result, err := provisioner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify Overrides
	if mockDB.ProvisionOpts.DatabaseName != "custom_db_name" {
		t.Errorf("expected db name override to 'custom_db_name', got '%s'", mockDB.ProvisionOpts.DatabaseName)
	}
	if mockDB.ProvisionOpts.DatabaseUser != "custom_user" {
		t.Errorf("expected db user override to 'custom_user', got '%s'", mockDB.ProvisionOpts.DatabaseUser)
	}
	if mockDB.ProvisionOpts.DatabasePassword != "StaticPassword123!" {
		t.Errorf("expected password override to work")
	}

	// Verify Result reflects the request
	if result.DatabaseName != "custom_db_name" {
		t.Errorf("result object mismatch")
	}
}
