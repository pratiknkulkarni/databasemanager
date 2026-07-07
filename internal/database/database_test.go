package database

import (
	"context"
	"strings"
	"testing"
)

func validOptions() ProvisionOptions {
	return ProvisionOptions{
		AppName:          "myapp",
		DatabaseName:     "myapp_db",
		DatabaseUser:     "myapp_user",
		DatabasePassword: "s3cret",
		DatabaseHostname: "localhost",
		DatabasePort:     5432,
		Schema:           "public",
	}
}

func TestProvisionOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ProvisionOptions)
		wantErr string
	}{
		{"valid", func(o *ProvisionOptions) {}, ""},
		{"valid without schema", func(o *ProvisionOptions) { o.Schema = "" }, ""},
		{"empty database name", func(o *ProvisionOptions) { o.DatabaseName = "" }, "database name must not be empty"},
		{"hyphen in database name", func(o *ProvisionOptions) { o.DatabaseName = "my-db" }, "invalid characters"},
		{"leading digit", func(o *ProvisionOptions) { o.DatabaseName = "1abc" }, "invalid characters"},
		{"sql metacharacters", func(o *ProvisionOptions) { o.DatabaseName = `db"; DROP TABLE x;--` }, "invalid characters"},
		{"database name too long", func(o *ProvisionOptions) { o.DatabaseName = strings.Repeat("a", 64) }, "exceeds 63 characters"},
		{"empty user", func(o *ProvisionOptions) { o.DatabaseUser = "" }, "database user must not be empty"},
		{"user too long", func(o *ProvisionOptions) { o.DatabaseUser = strings.Repeat("u", 33) }, "exceeds 32 characters"},
		{"bad schema", func(o *ProvisionOptions) { o.Schema = "public;DROP" }, "invalid characters"},
		{"empty password", func(o *ProvisionOptions) { o.DatabasePassword = "" }, "database password must not be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := validOptions()
			tt.mutate(&opts)
			err := opts.Validate()
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

func TestTestAppConnection_RejectsBadInput(t *testing.T) {
	fullCreds := Credentials{
		Type: "oracle", Host: "h", Port: "1", Name: "n", User: "u", Password: "p",
	}

	t.Run("unsupported type", func(t *testing.T) {
		err := TestAppConnection(context.Background(), fullCreds, "")
		want := "unsupported database type: oracle (supported: mysql, postgres)"
		if err == nil || err.Error() != want {
			t.Errorf("expected %q, got %v", want, err)
		}
	})

	t.Run("missing core fields", func(t *testing.T) {
		err := TestAppConnection(context.Background(), Credentials{Type: EnginePostgres}, "")
		if err == nil || !strings.Contains(err.Error(), "required secrets not found") {
			t.Errorf("expected missing-secrets error, got %v", err)
		}
	})
}
