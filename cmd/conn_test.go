package cmd

import (
	"strings"
	"testing"
)

func testGenerator(dbType, schema string) *ConnectionStringGenerator {
	return &ConnectionStringGenerator{
		Host:     "db.example.com",
		Port:     "5433",
		Database: "myapp_db",
		User:     "myapp_user",
		Password: "supersecret123",
		Schema:   schema,
		DBType:   dbType,
	}
}

func TestGeneratePostgres(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		format  string
		want    string
		wantErr bool
	}{
		{
			name:   "uri with schema",
			schema: "public",
			format: "uri",
			want:   "postgresql://myapp_user:supersecret123@db.example.com:5433/myapp_db?search_path=public",
		},
		{
			name:   "uri without schema",
			schema: "",
			format: "uri",
			want:   "postgresql://myapp_user:supersecret123@db.example.com:5433/myapp_db",
		},
		{
			name:   "psql command",
			schema: "public",
			format: "psql",
			want:   "PGPASSWORD=supersecret123 psql -h db.example.com -p 5433 -d myapp_db -U myapp_user",
		},
		{
			name:   "env with schema",
			schema: "app",
			format: "env",
			want:   "DATABASE_URL=postgresql://myapp_user:supersecret123@db.example.com:5433/myapp_db?search_path=app",
		},
		{
			name:   "env without schema",
			schema: "",
			format: "env",
			want:   "DATABASE_URL=postgresql://myapp_user:supersecret123@db.example.com:5433/myapp_db",
		},
		{
			name:    "mysql format rejected",
			schema:  "public",
			format:  "mysql",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := testGenerator("postgres", tt.schema)
			got, err := g.GeneratePostgres(tt.format)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GeneratePostgres(%q) error = %v, wantErr %v", tt.format, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("GeneratePostgres(%q) = %q, want %q", tt.format, got, tt.want)
			}
		})
	}
}

func TestGenerateMySQL(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		want    string
		wantErr bool
	}{
		{
			name:   "uri",
			format: "uri",
			want:   "mysql://myapp_user:supersecret123@db.example.com:5433/myapp_db",
		},
		{
			name:   "mysql command",
			format: "mysql",
			want:   "mysql -h db.example.com -P 5433 -D myapp_db -u myapp_user -psupersecret123",
		},
		{
			name:   "env",
			format: "env",
			want:   "DATABASE_URL=mysql://myapp_user:supersecret123@db.example.com:5433/myapp_db",
		},
		{
			name:    "psql format rejected",
			format:  "psql",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := testGenerator("mysql", "")
			got, err := g.GenerateMySQL(tt.format)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GenerateMySQL(%q) error = %v, wantErr %v", tt.format, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("GenerateMySQL(%q) = %q, want %q", tt.format, got, tt.want)
			}
		})
	}
}

func TestGenerateDispatch(t *testing.T) {
	if _, err := testGenerator("postgres", "").Generate("uri"); err != nil {
		t.Errorf("Generate should dispatch to postgres: %v", err)
	}
	if _, err := testGenerator("mysql", "").Generate("uri"); err != nil {
		t.Errorf("Generate should dispatch to mysql: %v", err)
	}
	if _, err := testGenerator("oracle", "").Generate("uri"); err == nil {
		t.Error("Generate should reject unknown database type")
	}
}

func TestGenerateAll(t *testing.T) {
	pg, err := testGenerator("postgres", "public").GenerateAll()
	if err != nil {
		t.Fatalf("GenerateAll(postgres) error: %v", err)
	}
	if len(pg) != 3 {
		t.Errorf("GenerateAll(postgres) returned %d formats, want 3", len(pg))
	}

	my, err := testGenerator("mysql", "").GenerateAll()
	if err != nil {
		t.Fatalf("GenerateAll(mysql) error: %v", err)
	}
	if len(my) != 3 {
		t.Errorf("GenerateAll(mysql) returned %d formats, want 3", len(my))
	}
}

// TestGenerate_SpecialCharPasswordIsEncoded proves a password containing URI
// metacharacters cannot break out of the userinfo and smuggle host/query
// parameters — it is percent-encoded, so the host and database are unchanged.
func TestGenerate_SpecialCharPasswordIsEncoded(t *testing.T) {
	g := &ConnectionStringGenerator{
		Host: "db.example.com", Port: "5432", Database: "app_db",
		User: "app_user", Password: "p@ss/w?rd&x", DBType: "postgres",
	}
	got, err := g.GeneratePostgres("uri")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A naive fmt.Sprintf would yield ...:p@ss/w?rd&x@db.example.com... where the
	// first '@' ends the userinfo at "p" and "ss/w?rd&x" corrupts host/query.
	want := "postgresql://app_user:p%40ss%2Fw%3Frd&x@db.example.com:5432/app_db"
	if got != want {
		t.Errorf("GeneratePostgres(uri) = %q, want %q", got, want)
	}
	if !strings.Contains(got, "@db.example.com:5432/app_db") {
		t.Errorf("host/database were corrupted by the password: %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"supersecret123", "supersecret123"},
		{"db.example.com", "db.example.com"},
		{"", "''"},
		{"pa ss", "'pa ss'"},
		{"a;rm -rf /", "'a;rm -rf /'"},
		{"it's", `'it'\''s'`},
		{"$(whoami)", "'$(whoami)'"},
	}
	for _, tt := range tests {
		if got := shellQuote(tt.in); got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestGeneratePSQL_ShellSafe proves a password with shell metacharacters is
// quoted so it cannot execute when the psql line is pasted.
func TestGeneratePSQL_ShellSafe(t *testing.T) {
	g := &ConnectionStringGenerator{
		Host: "h", Port: "5432", Database: "db", User: "u",
		Password: "a;rm -rf /", DBType: "postgres",
	}
	got, err := g.GeneratePostgres("psql")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "PGPASSWORD='a;rm -rf /' psql -h h -p 5432 -d db -U u"
	if got != want {
		t.Errorf("GeneratePostgres(psql) = %q, want %q", got, want)
	}
}

// TestMaskByRegeneration proves that masking (used for table + clipboard
// preview) hides the password reliably even when it contains characters the old
// regex maskers would have leaked, and renders a clean "*****" marker.
func TestMaskByRegeneration(t *testing.T) {
	g := &ConnectionStringGenerator{
		Host: "h", Port: "5432", Database: "db", User: "u",
		Password: "p@ss word", DBType: "postgres",
	}

	all, err := g.maskedAll()
	if err != nil {
		t.Fatalf("maskedAll error: %v", err)
	}
	var uri string
	for _, f := range all {
		if strings.Contains(f.Value, "ss") || strings.Contains(f.Value, "word") || strings.Contains(f.Value, "%20") {
			t.Errorf("maskedAll leaked password in %s: %q", f.Name, f.Value)
		}
		if f.Name == "URI" {
			uri = f.Value
		}
	}
	if uri != "postgresql://u:*****@h:5432/db" {
		t.Errorf("masked URI = %q, want clean *****", uri)
	}
}

func TestMaskPasswordString(t *testing.T) {
	if got := maskPasswordString("abcd"); got != "***" {
		t.Errorf("short passwords fully masked: got %q", got)
	}
	if got := maskPasswordString("supersecret123"); got != "su***23" {
		t.Errorf("long passwords keep 2+2 edges: got %q", got)
	}
}

func TestMaskSecretValue(t *testing.T) {
	tests := []struct {
		key   string
		value string
		want  string
	}{
		{"DB_PASSWORD", "hunter2", "*****"},
		{"API_KEY", "abc", "*****"},
		{"CLIENT_SECRET", "abc", "*****"},
		{"AUTH_TOKEN", "abc", "*****"},
		{"DB_PWD", "abc", "*****"},          // additional sensitive keyword
		{"DB_CREDENTIAL", "abc", "*****"},   //
		{"db_password", "hunter2", "*****"}, // case-insensitive
		{"DB_HOST", "localhost", "localhost"},
		{"DB_PORT", "5432", "5432"},
		// Innocuous key, but the value embeds a password in a connection URI:
		// mask just the password, keep host/db visible.
		{"DB_URI", "postgres://u:s3cr3t@host:5432/db", "postgres://u:*****@host:5432/db"},
		{"DSN", "mysql://app:hunter2@10.0.0.1:3306/app", "mysql://app:*****@10.0.0.1:3306/app"},
	}

	for _, tt := range tests {
		if got := maskSecretValue(tt.key, tt.value); got != tt.want {
			t.Errorf("maskSecretValue(%q, %q) = %q, want %q", tt.key, tt.value, got, tt.want)
		}
	}

	if !strings.Contains(maskSecretValue("DB_USER", "app_user"), "app_user") {
		t.Error("non-sensitive keys must pass through")
	}
}
