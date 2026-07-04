package database

import (
	"reflect"
	"strings"
	"testing"
)

func TestCredentials_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		creds Credentials
	}{
		{
			name: "postgres with schema",
			creds: Credentials{
				Type: EnginePostgres, Host: "localhost", Port: "5432",
				Name: "app_db", User: "app_user", Password: "s3cret", Schema: "public",
			},
		},
		{
			name: "mysql without schema",
			creds: Credentials{
				Type: EngineMySQL, Host: "127.0.0.1", Port: "3306",
				Name: "app_db", User: "app_user", Password: "s3cret",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretMap := make(map[string]string)
			for _, kv := range tt.creds.ToSecrets() {
				secretMap[kv.Key] = kv.Value
			}
			got := ParseCredentials(secretMap)
			if !reflect.DeepEqual(got, tt.creds) {
				t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, tt.creds)
			}
		})
	}
}

func TestCredentials_ToSecrets_OmitsEmptySchema(t *testing.T) {
	creds := Credentials{Type: EngineMySQL, Host: "h", Port: "1", Name: "n", User: "u", Password: "p"}
	for _, kv := range creds.ToSecrets() {
		if kv.Key == SecretKeySchema {
			t.Errorf("expected DB_SCHEMA to be omitted when schema is empty, got value %q", kv.Value)
		}
	}
}

func TestCredentials_ToSecrets_DeterministicOrder(t *testing.T) {
	creds := Credentials{
		Type: EnginePostgres, Host: "h", Port: "1",
		Name: "n", User: "u", Password: "p", Schema: "s",
	}
	want := []string{
		SecretKeyType, SecretKeyName, SecretKeyUser,
		SecretKeyPassword, SecretKeyHost, SecretKeyPort, SecretKeySchema,
	}
	kvs := creds.ToSecrets()
	if len(kvs) != len(want) {
		t.Fatalf("expected %d secrets, got %d", len(want), len(kvs))
	}
	for i, kv := range kvs {
		if kv.Key != want[i] {
			t.Errorf("position %d: expected key %q, got %q", i, want[i], kv.Key)
		}
	}
}

func TestCredentials_ResolveType(t *testing.T) {
	tests := []struct {
		name    string
		dbType  string
		want    string
		wantErr string
	}{
		{"postgres", EnginePostgres, EnginePostgres, ""},
		{"mysql", EngineMySQL, EngineMySQL, ""},
		{"missing", "", "", "DB_TYPE secret not found"},
		{"invalid", "oracle", "", `invalid DB_TYPE value: "oracle"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Credentials{Type: tt.dbType}.ResolveType()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("expected %q, got %q", tt.want, got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCredentials_ValidateContract(t *testing.T) {
	full := func(schema string) Credentials {
		return Credentials{
			Host: "h", Port: "1", Name: "n", User: "u", Password: "p", Schema: schema,
		}
	}

	tests := []struct {
		name    string
		creds   Credentials
		dbType  string
		wantErr string
	}{
		{"postgres complete", full("public"), EnginePostgres, ""},
		{"mysql complete", full(""), EngineMySQL, ""},
		{"postgres missing schema", full(""), EnginePostgres, "DB_SCHEMA not found but DB_TYPE is 'postgres'"},
		{"mysql with schema", full("public"), EngineMySQL, "DB_SCHEMA found but DB_TYPE is 'mysql'"},
		{
			"missing core fields",
			Credentials{Host: "h", Name: "n"},
			EngineMySQL,
			"required secrets not found: DB_PORT, DB_USER, DB_PASSWORD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.creds.ValidateContract(tt.dbType)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}
