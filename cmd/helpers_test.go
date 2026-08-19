package cmd

import (
	"strings"
	"testing"

	"github.com/pratiknkulkarni/databasemanager/internal/database"
)

// TestResolveEngine pins the shared engine-resolution policy: the stored
// DB_TYPE is authoritative, --type is only a legacy fallback, and
// disagreement is an error (never a silent override).
func TestResolveEngine(t *testing.T) {
	tests := []struct {
		name     string
		stored   string
		typeFlag string
		want     string
		wantErr  string
	}{
		{"stored type wins", database.EnginePostgres, "", database.EnginePostgres, ""},
		{"flag agrees with stored", database.EngineMySQL, database.EngineMySQL, database.EngineMySQL, ""},
		{"legacy fallback", "", database.EngineMySQL, database.EngineMySQL, ""},
		{"legacy without flag", "", "", "", "DB_TYPE secret is missing; pass --type"},
		{"mismatch rejected", database.EnginePostgres, database.EngineMySQL, "", "DB_TYPE mismatch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveEngine(database.Credentials{Type: tt.stored}, tt.typeFlag, "myapp")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("expected engine %q, got %q", tt.want, got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}
