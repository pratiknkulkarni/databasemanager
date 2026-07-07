package services

import (
	"context"
	"errors"
	"io"
	"maps"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
)

// errFakeSync is the injected failure used across store-backed tests.
var errFakeSync = errors.New("injected secret store failure")

// --- Mocks ---

// rotateCall records one RotatePassword invocation.
type rotateCall struct {
	User     string
	Password string
}

// MockDB implements database.Database for testing purposes
type MockDB struct {
	ProvisionCalled bool
	ProvisionOpts   database.ProvisionOptions
	Report          database.ProvisionReport // returned by Provision (zero value = everything adopted)
	DeleteCalled    bool
	DeletedName     string
	DeletedUser     string
	RotateCalls     []rotateCall
	RotateErr       error
	RotateErrOnCall int // 1-based call number RotateErr fires on; 0 = every call
}

func (m *MockDB) Provision(_ context.Context, opts database.ProvisionOptions) (database.ProvisionReport, error) {
	m.ProvisionCalled = true
	m.ProvisionOpts = opts
	return m.Report, nil
}

func (m *MockDB) Delete(_ context.Context, databaseName, userName string) error {
	m.DeleteCalled = true
	m.DeletedName = databaseName
	m.DeletedUser = userName
	return nil
}

func (m *MockDB) RotatePassword(_ context.Context, userName, newPassword string) error {
	m.RotateCalls = append(m.RotateCalls, rotateCall{User: userName, Password: newPassword})
	if m.RotateErr != nil && (m.RotateErrOnCall == 0 || len(m.RotateCalls) == m.RotateErrOnCall) {
		return m.RotateErr
	}
	return nil
}

func (m *MockDB) Close() error {
	return nil
}

// fakeSecretStore is an in-memory SecretStore with per-operation error
// injection. It models a single app path (tests exercise one app at a time)
// and records the paths it was addressed with for assertion.
type fakeSecretStore struct {
	secrets map[string]string
	folders map[string]bool

	listErr, createErr, updateErr, deleteErr, folderErr error

	lastPath      string
	updatedKeys   []string
	createdKeys   []string
	deletedKeys   []string
	folderDeleted bool
}

func newFakeSecretStore(seed map[string]string) *fakeSecretStore {
	secrets := make(map[string]string, len(seed))
	maps.Copy(secrets, seed)
	return &fakeSecretStore{secrets: secrets, folders: map[string]bool{}}
}

func (f *fakeSecretStore) ListSecrets(_, path string) (map[string]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.lastPath = path
	out := make(map[string]string, len(f.secrets))
	maps.Copy(out, f.secrets)
	return out, nil
}

func (f *fakeSecretStore) CreateSecret(_, path string, kv database.SecretKV) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.lastPath = path
	f.secrets[kv.Key] = kv.Value
	f.createdKeys = append(f.createdKeys, kv.Key)
	return nil
}

func (f *fakeSecretStore) UpdateSecret(_, path string, kv database.SecretKV) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.lastPath = path
	f.secrets[kv.Key] = kv.Value
	f.updatedKeys = append(f.updatedKeys, kv.Key)
	return nil
}

func (f *fakeSecretStore) DeleteSecret(_, _, key string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.secrets, key)
	f.deletedKeys = append(f.deletedKeys, key)
	return nil
}

func (f *fakeSecretStore) CreateFolder(_, name string) error {
	if f.folderErr != nil {
		return f.folderErr
	}
	f.folders[name] = true
	return nil
}

func (f *fakeSecretStore) FolderExists(_, name string) (bool, error) {
	return f.folders[name], nil
}

func (f *fakeSecretStore) DeleteFolder(_, name string) error {
	delete(f.folders, name)
	f.folderDeleted = true
	return nil
}

func newTestProvisioner(mockDB *MockDB, engine string, engineCfg config.DatabaseConfig) *Provisioner {
	return NewProvisioner(ProvisionerDeps{
		DB:        mockDB,
		Secrets:   nil, // Intentionally nil: provisioning must not panic offline
		Logger:    log.New(io.Discard),
		Engine:    engine,
		EngineCfg: engineCfg,
	})
}

func newTestProvisionerWithStore(mockDB *MockDB, store SecretStore, engine string, engineCfg config.DatabaseConfig) *Provisioner {
	return NewProvisioner(ProvisionerDeps{
		DB:        mockDB,
		Secrets:   store,
		Logger:    log.New(io.Discard),
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

// TestProvisioner_Run_ReturnsCredentialsOnSyncFailure pins the Phase 0
// contract: when the database is created but the sync fails, Run returns the
// result ALONGSIDE the error — the result carries the only copy of the
// generated password and the caller must be able to surface it.
func TestProvisioner_Run_ReturnsCredentialsOnSyncFailure(t *testing.T) {
	mockDB := &MockDB{}
	store := newFakeSecretStore(nil)
	store.createErr = errFakeSync
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{
		DatabaseHostname: "localhost", DatabasePort: 5432,
	})

	result, err := p.Run(context.Background(), ProvisionRequest{AppName: "myapp"})
	if err == nil {
		t.Fatal("expected an error when the sync fails")
	}
	if result == nil {
		t.Fatal("expected a non-nil result carrying the credentials")
	}
	if result.Synced {
		t.Error("expected Synced=false")
	}
	if result.Credentials.Password == "" {
		t.Error("result must carry the generated password — it exists nowhere else")
	}
}

func TestProvisioner_ResolveApp(t *testing.T) {
	logger := log.New(io.Discard)

	t.Run("not found", func(t *testing.T) {
		p := NewProvisioner(ProvisionerDeps{Secrets: newFakeSecretStore(nil), Logger: logger})
		_, err := p.ResolveApp("ghost", "dev")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected not-found error, got %v", err)
		}
	})

	t.Run("incomplete secrets", func(t *testing.T) {
		p := NewProvisioner(ProvisionerDeps{
			Secrets: newFakeSecretStore(map[string]string{database.SecretKeyHost: "h"}),
			Logger:  logger,
		})
		_, err := p.ResolveApp("myapp", "dev")
		if err == nil || !strings.Contains(err.Error(), "incomplete secrets") {
			t.Errorf("expected incomplete-secrets error, got %v", err)
		}
	})

	t.Run("resolves recorded names", func(t *testing.T) {
		p := NewProvisioner(ProvisionerDeps{Secrets: newFakeSecretStore(seedSecrets()), Logger: logger})
		creds, err := p.ResolveApp("myapp", "dev")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if creds.Name != "myapp_db" || creds.User != "myapp_user" {
			t.Errorf("expected recorded names, got %+v", creds)
		}
	})
}

// TestProvisioner_Deprovision pins the cleanup contract after the SecretStore
// refactor: database drop first, then every secret, then the folder — and a
// post-drop failure names the partial state.
func TestProvisioner_Deprovision(t *testing.T) {
	logger := log.New(io.Discard)

	t.Run("full cleanup", func(t *testing.T) {
		mockDB := &MockDB{}
		store := newFakeSecretStore(seedSecrets())
		p := NewProvisioner(ProvisionerDeps{DB: mockDB, Secrets: store, Logger: logger})

		creds := database.ParseCredentials(seedSecrets())
		if err := p.Deprovision(context.Background(), "myapp", "dev", creds); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !mockDB.DeleteCalled || mockDB.DeletedName != "myapp_db" || mockDB.DeletedUser != "myapp_user" {
			t.Errorf("expected DB.Delete with recorded names, got %+v", mockDB)
		}
		if len(store.secrets) != 0 {
			t.Errorf("expected all secrets deleted, %d remain", len(store.secrets))
		}
		if !store.folderDeleted {
			t.Error("expected the app folder to be deleted")
		}
	})

	t.Run("partial failure is named", func(t *testing.T) {
		mockDB := &MockDB{}
		store := newFakeSecretStore(seedSecrets())
		store.deleteErr = errFakeSync
		p := NewProvisioner(ProvisionerDeps{DB: mockDB, Secrets: store, Logger: logger})

		err := p.Deprovision(context.Background(), "myapp", "dev", database.ParseCredentials(seedSecrets()))
		if err == nil || !strings.Contains(err.Error(), "database deleted, but failed to delete secret") {
			t.Errorf("expected partial-state error, got %v", err)
		}
		if !mockDB.DeleteCalled {
			t.Error("the database drop must happen before secret cleanup")
		}
	})
}
