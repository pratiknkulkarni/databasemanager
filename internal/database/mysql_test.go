package database

import (
	"context"
	"strings"
	"testing"
)

func TestEscapeMySQLLiteral(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "password123", "password123"},
		{"single quote", "pa'ss", `pa\'ss`},
		{"backslash", `pa\ss`, `pa\\ss`},
		{"trailing backslash", `pass\`, `pass\\`},
		{"quote and backslash", `p'a\s`, `p\'a\\s`},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeMySQLLiteral(tt.in); got != tt.want {
				t.Errorf("escapeMySQLLiteral(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestMySQLClient_escapeLiteral pins that escaping follows the server's SQL
// mode: under NO_BACKSLASH_ESCAPES a quote is doubled (not backslash-escaped),
// otherwise a lone '\” would let a quote terminate the literal early.
func TestMySQLClient_escapeLiteral(t *testing.T) {
	defaultMode := &MySQLClient{noBackslashEscapes: false}
	if got, want := defaultMode.escapeLiteral(`pa'ss`), `pa\'ss`; got != want {
		t.Errorf("default mode: escapeLiteral(%q) = %q, want %q", `pa'ss`, got, want)
	}

	noBackslash := &MySQLClient{noBackslashEscapes: true}
	if got, want := noBackslash.escapeLiteral(`pa'ss`), `pa''ss`; got != want {
		t.Errorf("NO_BACKSLASH_ESCAPES: escapeLiteral(%q) = %q, want %q", `pa'ss`, got, want)
	}
	// A backslash is an ordinary character under NO_BACKSLASH_ESCAPES and must
	// not be doubled (doing so would alter the stored value).
	if got, want := noBackslash.escapeLiteral(`p\a`), `p\a`; got != want {
		t.Errorf("NO_BACKSLASH_ESCAPES: escapeLiteral(%q) = %q, want %q", `p\a`, got, want)
	}
}

func TestMySQLTLSParam(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "skip-verify"},        // secure default: encrypt, no cert verify
		{"disable", "false"},       // explicit opt-out for local dev
		{"false", "false"},         //
		{"require", "skip-verify"}, //
		{"verify-full", "true"},    // full verification
		{"custom-cfg", "custom-cfg"},
	}
	for _, tt := range tests {
		if got := mysqlTLSParam(tt.in); got != tt.want {
			t.Errorf("mysqlTLSParam(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestMySQLDelete_RejectsBadIdentifiers proves the delete path gates
// Infisical-sourced names on the identifier allowlist before any SQL is built
// or any connection is dialled.
func TestMySQLDelete_RejectsBadIdentifiers(t *testing.T) {
	m := &MySQLClient{} // no db handle: validation must fail before dialling
	err := m.Delete(context.Background(), `db'; DROP DATABASE prod; --`, "user")
	if err == nil || !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("expected invalid-characters rejection, got %v", err)
	}
}

// TestMySQLRotate_RejectsBadInput mirrors the Postgres gate on the rotate
// path: Infisical-sourced user names and empty passwords are refused before
// any SQL or dial.
func TestMySQLRotate_RejectsBadInput(t *testing.T) {
	m := &MySQLClient{} // no db handle: validation must fail before dialling

	err := m.RotatePassword(context.Background(), `u'; DROP DATABASE prod; --`, "newpass")
	if err == nil || !strings.Contains(err.Error(), "invalid characters") {
		t.Errorf("expected invalid-characters rejection, got %v", err)
	}

	err = m.RotatePassword(context.Background(), "good_user", "")
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("expected empty-password rejection, got %v", err)
	}
}
