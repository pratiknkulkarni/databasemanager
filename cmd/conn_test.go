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

func TestMaskPassword(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "uri password",
			in:   "postgresql://user:supersecret123@host:5432/db",
			want: "postgresql://user:*****@host:5432/db",
		},
		{
			name: "psql command",
			in:   "PGPASSWORD=supersecret123 psql -h host -p 5432 -d db -U user",
			want: "PGPASSWORD=***** psql -h host -p 5432 -d db -U user",
		},
		{
			name: "mysql command inline -p",
			in:   "mysql -h host -P 3306 -D db -u user -psupersecret123",
			want: "mysql -h host -P 3306 -D db -u user -p*****",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskPassword(tt.in); got != tt.want {
				t.Errorf("maskPassword(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMaskPasswordMiddle(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "uri password keeps edges",
			in:   "postgresql://user:supersecret123@host:5432/db",
			want: "postgresql://user:su***23@host:5432/db",
		},
		{
			name: "pgpassword keeps edges",
			in:   "PGPASSWORD=supersecret123 psql -h host -p 5432 -d db -U user",
			want: "PGPASSWORD=su***23 psql -h host -p 5432 -d db -U user",
		},
		{
			name: "mysql inline -p keeps edges",
			in:   "mysql -h host -P 3306 -D db -u user -psupersecret123",
			want: "mysql -h host -P 3306 -D db -u user -psu***23",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskPasswordMiddle(tt.in); got != tt.want {
				t.Errorf("maskPasswordMiddle(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
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
		{"db_password", "hunter2", "*****"}, // case-insensitive
		{"DB_HOST", "localhost", "localhost"},
		{"DB_PORT", "5432", "5432"},
	}

	for _, tt := range tests {
		if got := maskSecretValue(tt.key, tt.value); got != tt.want {
			t.Errorf("maskSecretValue(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}

	if !strings.Contains(maskSecretValue("DB_USER", "app_user"), "app_user") {
		t.Error("non-sensitive keys must pass through")
	}
}
