package services

import (
	"context"
	"strings"
	"testing"

	"github.com/pratiknkulkarni/databasemanager/internal/config"
	"github.com/pratiknkulkarni/databasemanager/internal/database"
)

func desiredPostgres() []database.SecretKV {
	return database.Credentials{
		Type: database.EnginePostgres, Host: "localhost", Port: "5432",
		Name: "myapp_db", User: "myapp_user", Password: "newpass", Schema: "public",
	}.ToSecrets()
}

func keysOf(kvs []database.SecretKV) []string {
	keys := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		keys = append(keys, kv.Key)
	}
	return keys
}

func TestPlanSecretSync_AllNew(t *testing.T) {
	desired := desiredPostgres()
	plan := planSecretSync(map[string]string{}, desired)

	if len(plan.creates) != len(desired) {
		t.Fatalf("expected %d creates, got %d", len(desired), len(plan.creates))
	}
	if len(plan.updates) != 0 || len(plan.deletes) != 0 || plan.unchanged != 0 {
		t.Errorf("expected creates only, got %+v", plan)
	}
	// Creates preserve the contract's deterministic serialization order.
	for i, kv := range plan.creates {
		if kv.Key != desired[i].Key {
			t.Errorf("create order diverged at %d: %q != %q", i, kv.Key, desired[i].Key)
		}
	}
}

func TestPlanSecretSync_AllUnchanged(t *testing.T) {
	desired := desiredPostgres()
	existing := make(map[string]string, len(desired))
	for _, kv := range desired {
		existing[kv.Key] = kv.Value
	}

	plan := planSecretSync(existing, desired)
	if len(plan.creates) != 0 || len(plan.updates) != 0 || len(plan.deletes) != 0 {
		t.Errorf("expected a no-op plan, got %+v", plan)
	}
	if plan.unchanged != len(desired) {
		t.Errorf("expected %d unchanged, got %d", len(desired), plan.unchanged)
	}
}

// TestPlanSecretSync_Mixed models the crash-mid-sync remnant: some keys
// landed, some hold stale values, some never made it.
func TestPlanSecretSync_Mixed(t *testing.T) {
	existing := map[string]string{
		database.SecretKeyType:     database.EnginePostgres, // unchanged
		database.SecretKeyName:     "myapp_db",              // unchanged
		database.SecretKeyPassword: "oldpass",               // stale → update
		// DB_USER, DB_HOST, DB_PORT, DB_SCHEMA missing → create
	}

	plan := planSecretSync(existing, desiredPostgres())

	if got := keysOf(plan.updates); len(got) != 1 || got[0] != database.SecretKeyPassword {
		t.Errorf("expected exactly DB_PASSWORD updated, got %v", got)
	}
	wantCreates := []string{
		database.SecretKeyUser, database.SecretKeyHost,
		database.SecretKeyPort, database.SecretKeySchema,
	}
	got := keysOf(plan.creates)
	if len(got) != len(wantCreates) {
		t.Fatalf("expected creates %v, got %v", wantCreates, got)
	}
	for i, key := range wantCreates {
		if got[i] != key {
			t.Errorf("create %d: expected %q, got %q", i, key, got[i])
		}
	}
	if plan.unchanged != 2 || len(plan.deletes) != 0 {
		t.Errorf("expected 2 unchanged and no deletes, got %+v", plan)
	}
}

// TestPlanSecretSync_DeletesStaleContractKeys pins that a contract key left
// over from the app's past (DB_SCHEMA when the desired contract is mysql) is
// removed — otherwise every reader's contract validation rejects the app.
func TestPlanSecretSync_DeletesStaleContractKeys(t *testing.T) {
	desiredMySQL := database.Credentials{
		Type: database.EngineMySQL, Host: "h", Port: "3306",
		Name: "myapp_db", User: "myapp_user", Password: "p",
	}.ToSecrets()

	existing := map[string]string{
		database.SecretKeySchema: "public", // stale: mysql apps carry no schema
	}

	plan := planSecretSync(existing, desiredMySQL)
	if len(plan.deletes) != 1 || plan.deletes[0] != database.SecretKeySchema {
		t.Errorf("expected stale DB_SCHEMA to be deleted, got %v", plan.deletes)
	}
}

// TestPlanSecretSync_PreservesOperatorKeys pins the safety boundary: keys
// outside the contract are operator-owned and must never be deleted.
func TestPlanSecretSync_PreservesOperatorKeys(t *testing.T) {
	existing := map[string]string{
		"MY_CUSTOM_API_KEY": "operator-owned",
		"REDIS_URL":         "redis://cache:6379",
	}

	plan := planSecretSync(existing, desiredPostgres())
	if len(plan.deletes) != 0 {
		t.Errorf("operator-owned keys must never be deleted, got deletes=%v", plan.deletes)
	}
}

// TestProvisioner_Run_FailsFastOnRecordedApp pins the pre-flight: an app that
// already has contract secrets is refused BEFORE any database mutation.
func TestProvisioner_Run_FailsFastOnRecordedApp(t *testing.T) {
	mockDB := &MockDB{}
	store := newFakeSecretStore(seedSecrets())
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{
		DatabaseHostname: "localhost", DatabasePort: 5432,
	})

	_, err := p.Run(context.Background(), ProvisionRequest{AppName: "myapp"})
	if err == nil || !strings.Contains(err.Error(), "already has recorded secrets") {
		t.Fatalf("expected already-recorded rejection, got %v", err)
	}
	if mockDB.ProvisionCalled {
		t.Error("the database must not be touched when the app is already recorded")
	}
}

// TestProvisioner_Run_FailsFastOnFolderError pins that a broken secret store
// (wrong environment slug, Infisical outage) fails while there is still
// nothing to clean up — previously this produced an orphaned database.
func TestProvisioner_Run_FailsFastOnFolderError(t *testing.T) {
	mockDB := &MockDB{}
	store := newFakeSecretStore(nil)
	store.folderErr = errFakeSync
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{
		DatabaseHostname: "localhost", DatabasePort: 5432,
	})

	_, err := p.Run(context.Background(), ProvisionRequest{AppName: "myapp"})
	if err == nil || !strings.Contains(err.Error(), "failed to create infisical folder") {
		t.Fatalf("expected folder-creation error, got %v", err)
	}
	if mockDB.ProvisionCalled {
		t.Error("the database must not be touched when the secret store is unusable")
	}
}
