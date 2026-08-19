package services

import (
	"context"
	"strings"
	"testing"

	"github.com/pratiknkulkarni/databasemanager/internal/config"
	"github.com/pratiknkulkarni/databasemanager/internal/database"
)

// TestProvisioner_Run_Adopt_ReconcilesRecordedApp drives the State A/B
// recovery: an app whose database exists and whose secrets are (partially or
// fully) recorded converges — the flag reaches the adapter, the fresh
// password lands in Infisical, unchanged facts are not rewritten, and
// operator-owned keys survive.
func TestProvisioner_Run_Adopt_ReconcilesRecordedApp(t *testing.T) {
	mockDB := &MockDB{} // zero Report: both resources pre-existed (adopted)
	seed := seedSecrets()
	seed["REDIS_URL"] = "redis://cache:6379" // operator-owned extra
	store := newFakeSecretStore(seed)
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{
		DatabaseHostname: "localhost", DatabasePort: 5432,
	})

	result, err := p.Run(context.Background(), ProvisionRequest{AppName: "myapp", Adopt: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !mockDB.ProvisionCalled || !mockDB.ProvisionOpts.Adopt {
		t.Error("expected the Adopt flag to reach the engine adapter")
	}
	if result.Report.DatabaseCreated || result.Report.UserCreated {
		t.Errorf("expected the result to report adoption, got %+v", result.Report)
	}

	// The generated password differs from the recorded one — exactly one
	// update; the six matching facts must not be rewritten.
	if result.SecretsUpdated != 1 || result.SecretsCreated != 0 || result.SecretsDeleted != 0 {
		t.Errorf("expected 1 update / 0 creates / 0 deletes, got %+v", result)
	}
	if result.SecretsUnchanged != 6 {
		t.Errorf("expected 6 unchanged facts, got %d", result.SecretsUnchanged)
	}
	if store.secrets[database.SecretKeyPassword] != result.Credentials.Password {
		t.Error("Infisical must hold the same fresh password the database received")
	}
	if store.secrets["REDIS_URL"] != "redis://cache:6379" {
		t.Error("operator-owned keys must survive adoption")
	}
}

// TestProvisioner_Run_Adopt_TypeMismatchRejected pins that adoption converges
// an app but never converts it across engines — and that the rejection fires
// BEFORE any database mutation.
func TestProvisioner_Run_Adopt_TypeMismatchRejected(t *testing.T) {
	mockDB := &MockDB{}
	store := newFakeSecretStore(map[string]string{
		database.SecretKeyType: database.EngineMySQL,
	})
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{
		DatabaseHostname: "localhost", DatabasePort: 5432,
	})

	_, err := p.Run(context.Background(), ProvisionRequest{AppName: "myapp", Adopt: true})
	if err == nil || !strings.Contains(err.Error(), "refusing to adopt as postgres") {
		t.Fatalf("expected engine-mismatch rejection, got %v", err)
	}
	if mockDB.ProvisionCalled {
		t.Error("the database must not be touched on an engine mismatch")
	}
}

// TestProvisioner_Run_Adopt_UpgradesLegacyApp pins the legacy path: an app
// recorded before DB_TYPE existed adopts cleanly and gains the full contract.
func TestProvisioner_Run_Adopt_UpgradesLegacyApp(t *testing.T) {
	mockDB := &MockDB{}
	seed := seedSecrets()
	delete(seed, database.SecretKeyType)
	store := newFakeSecretStore(seed)
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{
		DatabaseHostname: "localhost", DatabasePort: 5432,
	})

	result, err := p.Run(context.Background(), ProvisionRequest{AppName: "myapp", Adopt: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.secrets[database.SecretKeyType] != database.EnginePostgres {
		t.Errorf("expected DB_TYPE to be recorded, got %q", store.secrets[database.SecretKeyType])
	}
	if result.SecretsCreated != 1 { // DB_TYPE
		t.Errorf("expected exactly the DB_TYPE create, got %d creates", result.SecretsCreated)
	}
}

// TestProvisioner_Run_Adopt_HealsMissingDatabase drives State C: secrets
// exist but the database is gone. Adoption recreates it and updates the
// recorded password to the new reality.
func TestProvisioner_Run_Adopt_HealsMissingDatabase(t *testing.T) {
	mockDB := &MockDB{Report: database.ProvisionReport{DatabaseCreated: true, UserCreated: true}}
	store := newFakeSecretStore(seedSecrets())
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{
		DatabaseHostname: "localhost", DatabasePort: 5432,
	})

	result, err := p.Run(context.Background(), ProvisionRequest{AppName: "myapp", Adopt: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Report.DatabaseCreated || !result.Report.UserCreated {
		t.Errorf("expected recreation to be reported, got %+v", result.Report)
	}
	if store.secrets[database.SecretKeyPassword] != result.Credentials.Password {
		t.Error("recorded password must match the freshly created database")
	}
}
