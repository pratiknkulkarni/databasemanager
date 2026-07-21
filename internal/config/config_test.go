package config

import (
	"os"
	"testing"
)

// TestLoad_EnvOnly verifies configuration is loadable purely from environment
// variables, with no config file present (explicit BindEnv keys).
func TestLoad_EnvOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // ensure no real config file is picked up
	t.Setenv("DATABASEMANAGER_INFISICAL_PROJECT_ID", "proj_env")
	t.Setenv("DATABASEMANAGER_INFISICAL_CLIENT_ID", "client_env")
	t.Setenv("DATABASEMANAGER_INFISICAL_CLIENT_SECRET", "secret_env")
	t.Setenv("DATABASEMANAGER_POSTGRES_DATABASE_HOSTNAME", "envhost")
	t.Setenv("DATABASEMANAGER_POSTGRES_DATABASE_PORT", "5433")
	t.Setenv("DATABASEMANAGER_POSTGRES_DATABASE_USER", "envuser")
	t.Setenv("DATABASEMANAGER_POSTGRES_DATABASE_PASSWORD", "envpass")
	t.Setenv("DATABASEMANAGER_POSTGRES_DATABASE_NAME", "envdb")

	tmp := t.TempDir()
	oldWd, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load from env failed: %v", err)
	}
	if cfg.Postgres.DatabaseHostname != "envhost" {
		t.Errorf("expected hostname from env, got %q", cfg.Postgres.DatabaseHostname)
	}
	if cfg.Postgres.DatabasePort != 5433 {
		t.Errorf("expected port 5433 from env, got %d", cfg.Postgres.DatabasePort)
	}
	if cfg.InfisicalProjectID != "proj_env" {
		t.Errorf("expected project id from env, got %q", cfg.InfisicalProjectID)
	}
}

func TestConfig_Validate(t *testing.T) {
	type fields struct {
		Postgres              DatabaseConfig
		MySQL                 DatabaseConfig
		InfisicalProjectID    string
		InfisicalClientID     string
		InfisicalClientSecret string
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name: "Valid Config (Postgres only)",
			fields: fields{
				InfisicalProjectID:    "proj_123",
				InfisicalClientID:     "client_123",
				InfisicalClientSecret: "secret_123",
				Postgres: DatabaseConfig{
					DatabaseHostname: "localhost",
					DatabasePort:     5432,
					DatabaseUser:     "admin",
					DatabasePassword: "password",
					DatabaseName:     "postgres",
				},
			},
			wantErr: false,
		},
		{
			name: "Missing Infisical Details",
			fields: fields{
				InfisicalProjectID: "",
			},
			wantErr: true,
		},
		{
			name: "Incomplete Database Config",
			fields: fields{
				InfisicalProjectID:    "proj_123",
				InfisicalClientID:     "client_123",
				InfisicalClientSecret: "secret_123",
				Postgres: DatabaseConfig{
					DatabaseHostname: "localhost",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{
				Postgres:              tt.fields.Postgres,
				MySQL:                 tt.fields.MySQL,
				InfisicalProjectID:    tt.fields.InfisicalProjectID,
				InfisicalClientID:     tt.fields.InfisicalClientID,
				InfisicalClientSecret: tt.fields.InfisicalClientSecret,
			}
			if err := c.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestConfig_Validate_AuthMethods covers the two accepted machine-identity
// credential shapes and the misconfiguration that motivated them: a Token Auth
// access token pasted into the Universal Auth client-secret field, which the
// API rejects as an opaque 401.
func TestConfig_Validate_AuthMethods(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJhdXRoTWV0aG9kIjoidG9rZW4tYXV0aCJ9.sig"

	tests := []struct {
		name       string
		cfg        Config
		wantErr    bool
		wantMethod string
	}{
		{
			name:       "universal auth pair is accepted",
			cfg:        Config{InfisicalProjectID: "proj", InfisicalClientID: "id", InfisicalClientSecret: "secret"},
			wantMethod: "universal-auth",
		},
		{
			name:       "access token alone is accepted",
			cfg:        Config{InfisicalProjectID: "proj", InfisicalAccessToken: jwt},
			wantMethod: "token-auth",
		},
		{
			name:       "access token takes precedence over the pair",
			cfg:        Config{InfisicalProjectID: "proj", InfisicalClientID: "id", InfisicalClientSecret: "secret", InfisicalAccessToken: jwt},
			wantMethod: "token-auth",
		},
		{
			name:    "jwt in the client secret field is rejected",
			cfg:     Config{InfisicalProjectID: "proj", InfisicalClientID: "id", InfisicalClientSecret: jwt},
			wantErr: true,
		},
		{
			name:    "no credentials at all is rejected",
			cfg:     Config{InfisicalProjectID: "proj"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got := tt.cfg.AuthMethod(); got != tt.wantMethod {
				t.Errorf("AuthMethod() = %q, want %q", got, tt.wantMethod)
			}
		})
	}
}
