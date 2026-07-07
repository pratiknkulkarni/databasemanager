package services

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
)

// seedSecrets is a complete recorded postgres app, as ResolveApp would find it.
func seedSecrets() map[string]string {
	return map[string]string{
		database.SecretKeyType:     database.EnginePostgres,
		database.SecretKeyName:     "myapp_db",
		database.SecretKeyUser:     "myapp_user",
		database.SecretKeyPassword: "oldpass",
		database.SecretKeyHost:     "localhost",
		database.SecretKeyPort:     "5432",
		database.SecretKeySchema:   "public",
	}
}

func TestProvisioner_Rotate_GeneratesAndRecordsPassword(t *testing.T) {
	mockDB := &MockDB{}
	store := newFakeSecretStore(seedSecrets())
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{})

	result, err := p.Rotate(context.Background(), "myapp", "dev", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mockDB.RotateCalls) != 1 {
		t.Fatalf("expected exactly one RotatePassword call, got %d", len(mockDB.RotateCalls))
	}
	call := mockDB.RotateCalls[0]

	// The recorded user name is what rotates — never a name derived from the
	// app name.
	if call.User != "myapp_user" {
		t.Errorf("expected rotation of recorded user %q, got %q", "myapp_user", call.User)
	}

	// Default password: 16 crypto/rand bytes hex-encoded.
	if len(call.Password) != 32 {
		t.Errorf("expected 32-char generated password, got %d chars", len(call.Password))
	}
	if _, err := hex.DecodeString(call.Password); err != nil {
		t.Errorf("generated password is not hex: %v", err)
	}

	// The DB and Infisical must hold the SAME new password.
	if !result.Synced {
		t.Error("expected Synced=true")
	}
	if store.secrets[database.SecretKeyPassword] != call.Password {
		t.Errorf("Infisical DB_PASSWORD %q does not match the password applied to the database %q",
			store.secrets[database.SecretKeyPassword], call.Password)
	}
	if result.Credentials.Password != call.Password {
		t.Error("result credentials must carry the new password")
	}

	// An existing secret is updated in place, not re-created.
	if len(store.updatedKeys) != 1 || store.updatedKeys[0] != database.SecretKeyPassword {
		t.Errorf("expected exactly one update of DB_PASSWORD, got updates=%v creates=%v",
			store.updatedKeys, store.createdKeys)
	}
}

func TestProvisioner_Rotate_HonoursOverride(t *testing.T) {
	mockDB := &MockDB{}
	store := newFakeSecretStore(seedSecrets())
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{})

	_, err := p.Rotate(context.Background(), "myapp", "dev", "MyStr0ng!Pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := mockDB.RotateCalls[0].Password; got != "MyStr0ng!Pass" {
		t.Errorf("expected override password to reach the database verbatim, got %q", got)
	}
}

func TestProvisioner_Rotate_RequiresInfisical(t *testing.T) {
	mockDB := &MockDB{}
	p := newTestProvisioner(mockDB, database.EnginePostgres, config.DatabaseConfig{})

	_, err := p.Rotate(context.Background(), "myapp", "dev", "")
	if err == nil || !strings.Contains(err.Error(), "infisical client not initialized") {
		t.Errorf("expected infisical-required error, got %v", err)
	}
	if len(mockDB.RotateCalls) != 0 {
		t.Error("expected no database mutation without Infisical")
	}
}

// TestProvisioner_Rotate_RollsBackOnSyncFailure pins the two-resource
// consistency contract: if the DB_PASSWORD update fails, the database is
// restored to the old password so a failed rotation changes nothing.
func TestProvisioner_Rotate_RollsBackOnSyncFailure(t *testing.T) {
	mockDB := &MockDB{}
	store := newFakeSecretStore(seedSecrets())
	store.updateErr = errors.New("infisical is down")
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{})

	result, err := p.Rotate(context.Background(), "myapp", "dev", "")
	if err == nil {
		t.Fatal("expected an error when the secret update fails")
	}
	if result == nil || !result.RolledBack {
		t.Fatalf("expected RolledBack=true, got %+v", result)
	}

	if len(mockDB.RotateCalls) != 2 {
		t.Fatalf("expected apply + rollback = 2 RotatePassword calls, got %d", len(mockDB.RotateCalls))
	}
	if got := mockDB.RotateCalls[1].Password; got != "oldpass" {
		t.Errorf("rollback must restore the OLD password, got %q", got)
	}
	if store.secrets[database.SecretKeyPassword] != "oldpass" {
		t.Error("Infisical must still hold the old password after a rolled-back rotation")
	}
}

// TestProvisioner_Rotate_SurfacesPasswordWhenRollbackFails pins the
// last-resort path: DB holds the new password, Infisical the old one, and the
// rollback failed too — the result must carry the new password so the caller
// can print the only copy.
func TestProvisioner_Rotate_SurfacesPasswordWhenRollbackFails(t *testing.T) {
	mockDB := &MockDB{RotateErr: errors.New("connection lost"), RotateErrOnCall: 2}
	store := newFakeSecretStore(seedSecrets())
	store.updateErr = errors.New("infisical is down")
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{})

	result, err := p.Rotate(context.Background(), "myapp", "dev", "")
	if err == nil || !strings.Contains(err.Error(), "keeps the new password") {
		t.Fatalf("expected partial-state error, got %v", err)
	}
	if result == nil || result.RolledBack {
		t.Fatalf("expected RolledBack=false, got %+v", result)
	}
	if result.Credentials.Password != mockDB.RotateCalls[0].Password {
		t.Error("result must carry the live (new) password — it exists nowhere else")
	}
}

// TestProvisioner_Rotate_RecreatesMissingPasswordSecret pins the recovery
// use-case: a lost/deleted DB_PASSWORD secret does not block rotation — a
// fresh one is issued and created.
func TestProvisioner_Rotate_RecreatesMissingPasswordSecret(t *testing.T) {
	seed := seedSecrets()
	delete(seed, database.SecretKeyPassword)
	mockDB := &MockDB{}
	store := newFakeSecretStore(seed)
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{})

	result, err := p.Rotate(context.Background(), "myapp", "dev", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Synced {
		t.Error("expected Synced=true")
	}
	if len(store.createdKeys) != 1 || store.createdKeys[0] != database.SecretKeyPassword {
		t.Errorf("expected DB_PASSWORD to be created (not updated), creates=%v updates=%v",
			store.createdKeys, store.updatedKeys)
	}
}

// TestProvisioner_Rotate_BackfillsLegacyType pins that a legacy app (no
// DB_TYPE) rotated via the --type fallback yields credentials carrying the
// resolved engine, so the post-rotation connectivity check can dial.
func TestProvisioner_Rotate_BackfillsLegacyType(t *testing.T) {
	seed := seedSecrets()
	delete(seed, database.SecretKeyType)
	mockDB := &MockDB{}
	store := newFakeSecretStore(seed)
	p := newTestProvisionerWithStore(mockDB, store, database.EnginePostgres, config.DatabaseConfig{})

	result, err := p.Rotate(context.Background(), "myapp", "dev", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Credentials.Type != database.EnginePostgres {
		t.Errorf("expected engine backfilled onto credentials, got %q", result.Credentials.Type)
	}
}
