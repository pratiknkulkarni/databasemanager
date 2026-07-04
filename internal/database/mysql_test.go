package database

import "testing"

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
