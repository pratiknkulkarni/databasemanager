package database

import (
	"context"
	"strings"
	"testing"
	"time"

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
	want := `host='db.internal' port=5433 user='admin' password='x host=evil.example' dbname='appdb' sslmode='require' connect_timeout=10`
	if got != want {
		t.Errorf("dsn() = %q\nwant %q", got, want)
	}
}

// TestPostgresDSN_BoundsTheDial pins the connect timeout onto the admin DSN.
// Without it, dialling a host that drops packets rather than refusing them
// blocks on the OS default and the CLI hangs with no output instead of
// reporting the server unreachable.
func TestPostgresDSN_BoundsTheDial(t *testing.T) {
	p := newPostgresClient(config.DatabaseConfig{
		DatabaseHostname: "h", DatabasePort: 5432, DatabaseUser: "u", DatabasePassword: "p",
	})
	if got := p.dsn("d"); !strings.Contains(got, "connect_timeout=10") {
		t.Errorf("admin DSN must bound the dial, got %q", got)
	}
}

// TestPostgresAppConnection_BoundsTheDial covers the same property on the
// app-credential probe, which builds its DSN through url.URL rather than dsn().
// A `test` that hangs is worse than one that fails: it reports nothing at all.
func TestPostgresAppConnection_BoundsTheDial(t *testing.T) {
	// 203.0.113.0/24 is TEST-NET-3: reserved for documentation, so the dial
	// gets no response rather than a refusal, which is the case that hung.
	creds := Credentials{
		Type: EnginePostgres, Name: "d", User: "u", Password: "p",
		Host: "203.0.113.1", Port: "5432",
	}

	start := time.Now()
	err := testPostgresAppConnection(context.Background(), creds, "disable")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a connection failure against an unroutable host")
	}
	if elapsed > 15*time.Second {
		t.Errorf("dial was not bounded: took %v, want under the 5s budget plus slack", elapsed)
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
