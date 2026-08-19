package database

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pratiknkulkarni/databasemanager/internal/config"
)

func TestEngines_Sorted(t *testing.T) {
	want := []string{EngineMySQL, EnginePostgres}
	if got := Engines(); !reflect.DeepEqual(got, want) {
		t.Errorf("Engines() = %v, want %v", got, want)
	}
}

func TestNew(t *testing.T) {
	configured := config.DatabaseConfig{
		DatabaseHostname: "localhost",
		DatabasePort:     5432,
		DatabaseUser:     "admin",
		DatabasePassword: "pw",
		DatabaseName:     "postgres",
	}

	t.Run("unknown engine", func(t *testing.T) {
		_, err := New("oracle", configured)
		want := "unsupported database type: oracle (supported: mysql, postgres)"
		if err == nil || err.Error() != want {
			t.Errorf("expected %q, got %v", want, err)
		}
	})

	t.Run("engine not configured", func(t *testing.T) {
		_, err := New(EngineMySQL, config.DatabaseConfig{})
		if err == nil || !strings.Contains(err.Error(), "mysql is not configured") {
			t.Errorf("expected not-configured error, got %v", err)
		}
	})

	t.Run("configured engines construct", func(t *testing.T) {
		for _, engine := range Engines() {
			db, err := New(engine, configured)
			if err != nil {
				t.Errorf("New(%q) unexpected error: %v", engine, err)
				continue
			}
			if db == nil {
				t.Errorf("New(%q) returned nil adapter", engine)
			}
		}
	})
}
