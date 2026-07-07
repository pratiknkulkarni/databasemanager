package database

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// identifierPattern is the allowlist for SQL identifiers (database, user, and
// schema names). Identifiers are additionally quoted per engine, but names are
// restricted up front so they behave identically across engines and shells.
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	// maxIdentifierLen is PostgreSQL's identifier limit; MySQL allows 64 for
	// schema objects, so the lower bound applies to both.
	maxIdentifierLen = 63
	// maxUserLen is MySQL's user name limit (PostgreSQL allows 63).
	maxUserLen = 32
)

// ProvisionOptions holds the provisioning options when user is trying to provision or create a new database.
type ProvisionOptions struct {
	AppName          string
	DatabasePassword string
	DatabaseName     string
	DatabaseUser     string
	DatabasePort     int
	DatabaseHostname string
	Schema           string
}

// Validate verifies the provisioning options before any SQL is built from
// them: identifiers must match the allowlist and fit both engines' limits,
// and a password must be present.
func (po *ProvisionOptions) Validate() error {
	if err := validateIdentifier("database name", po.DatabaseName, maxIdentifierLen); err != nil {
		return err
	}
	if err := validateIdentifier("database user", po.DatabaseUser, maxUserLen); err != nil {
		return err
	}
	if po.Schema != "" {
		if err := validateIdentifier("schema", po.Schema, maxIdentifierLen); err != nil {
			return err
		}
	}
	if po.DatabasePassword == "" {
		return fmt.Errorf("database password must not be empty")
	}
	return nil
}

func validateIdentifier(what, value string, maxLen int) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", what)
	}
	if len(value) > maxLen {
		return fmt.Errorf("%s %q exceeds %d characters", what, value, maxLen)
	}
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%s %q contains invalid characters (allowed: letters, digits, underscore; must not start with a digit)", what, value)
	}
	return nil
}

// provisionState tracks the resources created during provisioning so a failed
// run can roll back exactly what it created.
type provisionState struct {
	databaseCreated bool
	userCreated     bool
}

// Database is the set of operations commands actually invoke on an engine.
// Connection management is an internal concern of each implementation.
type Database interface {
	Provision(ctx context.Context, provisionOptions ProvisionOptions) error
	Delete(ctx context.Context, databaseName, userName string) error
	// RotatePassword sets a new password for an existing provisioned user.
	// The user name arrives from Infisical, not the validated provision
	// path, so implementations re-apply the identifier allowlist before any
	// SQL is built — the same trust boundary as Delete.
	RotatePassword(ctx context.Context, userName, newPassword string) error
	Close() error
}

// TestAppConnection dials the database described by creds using the
// provisioned application credentials (not the admin connection) and pings it.
// sslMode selects the transport-security posture (empty means the engine's
// secure default); it is sourced from the engine's configuration section so an
// operator can relax it for local development without editing code.
func TestAppConnection(ctx context.Context, creds Credentials, sslMode string) error {
	if err := creds.requireCore(); err != nil {
		return err
	}

	switch creds.Type {
	case EnginePostgres:
		return testPostgresAppConnection(ctx, creds, sslMode)
	case EngineMySQL:
		return testMySQLAppConnection(ctx, creds, sslMode)
	default:
		return fmt.Errorf("unsupported database type: %s (supported: %s)", creds.Type, strings.Join(Engines(), ", "))
	}
}
