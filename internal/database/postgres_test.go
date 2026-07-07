package database

import (
	"context"
	"strings"
	"testing"

	"github.com/praaatik/databasemanager/internal/config"
)

// TestPostgresDSN_QuotesValuesAndDefaultsSecure proves the admin DSN quotes
// every interpolated value (so a config value can't smuggle extra keywords)
// and that transport encryption is required unless explicitly configured off.
func TestPostgresDSN_QuotesValuesAndDefaultsSecure(t *testing.T) {
	p := newPostgresClient(config.DatabaseConfig{
		DatabaseHostname: "db.internal",
		DatabasePort:     5433,
		DatabaseUser:     "admin",
		// A password that, unquoted, would inject a second host= keyword and
		// redirect the admin dial.
		DatabasePassword: "x host=evil.example",
	})

	got := p.dsn("appdb")
	want := `host='db.internal' port=5433 user='admin' password='x host=evil.example' dbname='appdb' sslmode='require'`
	if got != want {
		t.Errorf("dsn() = %q\nwant %q", got, want)
	}
}

func TestPostgresDSN_HonoursConfiguredSSLMode(t *testing.T) {
	p := newPostgresClient(config.DatabaseConfig{
		DatabaseHostname: "h", DatabasePort: 5432, DatabaseUser: "u",
		DatabasePassword: "p", DatabaseSSLMode: "disable",
	})
	if got := p.dsn("d"); !strings.Contains(got, `sslmode='disable'`) {
		t.Errorf("expected configured sslmode=disable, got %q", got)
	}
}

// TestPostgresDelete_RejectsBadIdentifiers mirrors the MySQL gate: names read
// back from Infisical are re-validated before any SQL or dial.
func TestPostgresDelete_RejectsBadIdentifiers(t *testing.T) {
	p := &PostgresClient{} // no db handle: validation must fail before dialling
	err := p.Delete(context.Background(), "good_db", `u"; DROP ROLE x; --`)
	if err == nil || !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("expected invalid-characters rejection, got %v", err)
	}
}

// TestPostgresRotate_RejectsBadInput proves the rotate path applies the same
// pre-dial gates as Delete: the Infisical-sourced user name must pass the
// identifier allowlist, and an empty password is refused before any SQL.
func TestPostgresRotate_RejectsBadInput(t *testing.T) {
	p := &PostgresClient{} // no db handle: validation must fail before dialling

	err := p.RotatePassword(context.Background(), `u"; DROP ROLE x; --`, "newpass")
	if err == nil || !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("expected invalid-characters rejection, got %v", err)
	}

	err = p.RotatePassword(context.Background(), "good_user", "")
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("expected empty-password rejection, got %v", err)
	}
}
