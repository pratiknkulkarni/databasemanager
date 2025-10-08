package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
	"github.com/spf13/pflag"
)

func resetProvisionFlags() {
	provisionCmd.Flags().VisitAll(func(f *pflag.Flag) {
		switch f.Value.Type() {
		case "string":
			_ = f.Value.Set("")
		case "bool":
			_ = f.Value.Set("false")
		}
	})
}

type fakeDB struct {
	called bool
	last   database.ProvisionOptions
}

func (f *fakeDB) Connect(ctx context.Context) error { return nil }
func (f *fakeDB) Test(ctx context.Context) error    { return nil }
func (f *fakeDB) Provision(ctx context.Context, opts database.ProvisionOptions) error {
	f.called = true
	f.last = opts
	return nil
}

func TestProvisionCLI_UsesProvidedPassword(t *testing.T) {
	// preserve globals and restore after test
	oldAppName := appName
	oldAppPassword := appPassword
	defer func() {
		appName = oldAppName
		appPassword = oldAppPassword
	}()

	// default
	appName = "testapp"
	appPassword = "explicit-pass-123"

	// fake config and fake DB and set them into context
	cfg := &config.Config{
		DatabaseConfig: config.DatabaseConfig{
			DatabaseHostname: "localhost",
			DatabasePort:     5432,
			DatabaseUser:     "admin",
			DatabasePassword: "adminpw",
		},
	}
	fake := &fakeDB{}

	ctx := context.Background()
	ctx = context.WithValue(ctx, configKey{}, cfg)
	ctx = context.WithValue(ctx, databaseClientKey{}, fake)
	provisionCmd.SetContext(ctx)

	if err := provisionCmd.RunE(provisionCmd, []string{}); err != nil {
		t.Fatalf("provision command failed: %v", err)
	}

	if !fake.called {
		t.Fatalf("expected Provision to be called on fake DB")
	}

	if fake.last.AppPassword != "explicit-pass-123" {
		t.Fatalf("expected provided password to be passed to Provision; got %q", fake.last.AppPassword)
	}
}

func TestProvisionCLI_GeneratesRandomPasswordWhenNotProvided(t *testing.T) {
	oldAppName := appName
	oldAppPassword := appPassword
	defer func() {
		appName = oldAppName
		appPassword = oldAppPassword
	}()

	appName = "testapp2"
	appPassword = ""

	cfg := &config.Config{
		DatabaseConfig: config.DatabaseConfig{
			DatabaseHostname: "localhost",
			DatabasePort:     5432,
			DatabaseUser:     "admin",
			DatabasePassword: "adminpw",
		},
	}
	fake := &fakeDB{}

	ctx := context.Background()
	ctx = context.WithValue(ctx, configKey{}, cfg)
	ctx = context.WithValue(ctx, databaseClientKey{}, fake)
	provisionCmd.SetContext(ctx)

	// run the command (no DB connections will be made)
	if err := provisionCmd.RunE(provisionCmd, []string{}); err != nil {
		t.Fatalf("provision command failed: %v", err)
	}

	if !fake.called {
		t.Fatalf("expected Provision to be called on fake DB")
	}

	if fake.last.AppPassword == "" {
		t.Fatalf("expected generated password, got empty")
	}

	// our generator uses 16 bytes => hex is length 32
	if len(fake.last.AppPassword) != 32 {
		t.Fatalf("expected generated password length 32, got %d (value=%q)", len(fake.last.AppPassword), fake.last.AppPassword)
	}
}

func TestProvisionCLI_DefaultNames(t *testing.T) {
	resetProvisionFlags()
	fake := &fakeDB{}
	cfg := &config.Config{
		DatabaseConfig: config.DatabaseConfig{
			DatabaseHostname: "localhost",
			DatabasePort:     5432,
			DatabaseUser:     "admin",
			DatabasePassword: "adminpw",
		},
	}
	ctx := context.WithValue(context.Background(), configKey{}, cfg)
	ctx = context.WithValue(ctx, databaseClientKey{}, fake)
	provisionCmd.SetContext(ctx)

	// set only app and password flags (simulate CLI)
	provisionCmd.Flags().Set("app", "deriveapp")
	provisionCmd.Flags().Set("password", "p")

	if err := provisionCmd.RunE(provisionCmd, []string{}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if fake.last.Database != "deriveapp_db" {
		t.Fatalf("expected db deriveapp_db got %q", fake.last.Database)
	}
	if fake.last.User != "deriveapp_user" {
		t.Fatalf("expected user deriveapp_user got %q", fake.last.User)
	}
	if fake.last.Schema != "deriveapp_data" {
		t.Fatalf("expected schema deriveapp_data got %q", fake.last.Schema)
	}
}

func TestPrintProvisionSummary_HidePassword(t *testing.T) {
	var buf bytes.Buffer
	printProvisionSummary(&buf, "app", "db", "user", "schema", "mypw", true)
	if strings.Contains(buf.String(), "mypw") {
		t.Fatalf("password leaked in summary: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "*****") {
		t.Fatalf("expected masked password; got: %s", buf.String())
	}
}

func TestProvisionCLI_MissingAppFlag(t *testing.T) {
	provisionCmd.Flags().Set("app", "")
	provisionCmd.SetContext(context.Background())
	err := provisionCmd.RunE(provisionCmd, []string{})
	if err == nil {
		t.Fatal("expected error when --app not provided")
	}
}

func TestGenerateRandomPassword_LengthAndUniqueness(t *testing.T) {
	p1 := generateRandomPassword(16)
	p2 := generateRandomPassword(16)
	if len(p1) != 32 || len(p2) != 32 {
		t.Fatalf("unexpected lengths: %d, %d", len(p1), len(p2))
	}
	if p1 == p2 {
		t.Fatalf("expected random passwords to differ")
	}
}

func TestProvisionCLI_TableDrivenFlagCombinations(t *testing.T) {
	cases := []struct {
		name   string
		flags  map[string]string
		expect func(opts database.ProvisionOptions) error
	}{
		{
			name: "all overrides",
			flags: map[string]string{
				"app": "a", "password": "p", "dbname": "db", "user": "u", "schema": "s",
			},
			expect: func(opts database.ProvisionOptions) error {
				if opts.Database != "db" || opts.User != "u" || opts.Schema != "s" {
					return fmt.Errorf("unexpected opts: %+v", opts)
				}
				return nil
			},
		},
		{
			name:  "partial overrides",
			flags: map[string]string{"app": "b", "password": "p", "user": "onlyuser"},
			expect: func(opts database.ProvisionOptions) error {
				if opts.User != "onlyuser" {
					return fmt.Errorf("user not set")
				}

				if opts.Database != "b_db" {
					return fmt.Errorf("expected derived database b_db, got %q", opts.Database)
				}

				return nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetProvisionFlags()
			fake := &fakeDB{}
			cfg := &config.Config{ /*...*/ }
			ctx := context.WithValue(context.Background(), configKey{}, cfg)
			ctx = context.WithValue(ctx, databaseClientKey{}, fake)
			provisionCmd.SetContext(ctx)

			for k, v := range tc.flags {
				provisionCmd.Flags().Set(k, v)
			}

			if err := provisionCmd.RunE(provisionCmd, []string{}); err != nil {
				t.Fatalf("run failed: %v", err)
			}
			if err := tc.expect(fake.last); err != nil {
				t.Fatalf("expectation failed: %v", err)
			}
		})
	}
}
