package database

import (
	"fmt"
	"strings"
)

// Secret keys under which an app's connection facts are stored in Infisical.
// This is the single definition of the secret contract: the provisioner
// writes it via Credentials.ToSecrets and every reader parses it via
// ParseCredentials.
const (
	SecretKeyType     = "DB_TYPE"
	SecretKeyName     = "DB_NAME"
	SecretKeyUser     = "DB_USER"
	SecretKeyPassword = "DB_PASSWORD"
	SecretKeyHost     = "DB_HOST"
	SecretKeyPort     = "DB_PORT"
	SecretKeySchema   = "DB_SCHEMA"
)

// ContractKeys returns every key of the secret contract, in serialization
// order. It is the definitive list used to tell contract keys apart from
// operator-owned extras stored in the same folder — reconciliation may only
// ever delete keys named here.
func ContractKeys() []string {
	return []string{
		SecretKeyType, SecretKeyName, SecretKeyUser,
		SecretKeyPassword, SecretKeyHost, SecretKeyPort, SecretKeySchema,
	}
}

// Credentials is the set of connection facts recorded per provisioned app.
type Credentials struct {
	Type     string // EnginePostgres or EngineMySQL; empty for apps provisioned before DB_TYPE existed
	Host     string
	Port     string
	Name     string
	User     string
	Password string
	Schema   string // postgres only
}

// SecretKV is one key/value pair of the serialized secret contract.
type SecretKV struct {
	Key   string
	Value string
}

// ParseCredentials maps a raw secret map onto the credential contract.
// It performs no validation; callers check exactly what they need via
// ResolveType, ValidateContract, or direct field access.
func ParseCredentials(secrets map[string]string) Credentials {
	return Credentials{
		Type:     secrets[SecretKeyType],
		Host:     secrets[SecretKeyHost],
		Port:     secrets[SecretKeyPort],
		Name:     secrets[SecretKeyName],
		User:     secrets[SecretKeyUser],
		Password: secrets[SecretKeyPassword],
		Schema:   secrets[SecretKeySchema],
	}
}

// ToSecrets serializes the credentials into the secret contract, in
// deterministic order. DB_SCHEMA is only written when a schema is set.
func (c Credentials) ToSecrets() []SecretKV {
	kvs := []SecretKV{
		{SecretKeyType, c.Type},
		{SecretKeyName, c.Name},
		{SecretKeyUser, c.User},
		{SecretKeyPassword, c.Password},
		{SecretKeyHost, c.Host},
		{SecretKeyPort, c.Port},
	}
	if c.Schema != "" {
		kvs = append(kvs, SecretKV{SecretKeySchema, c.Schema})
	}
	return kvs
}

// ResolveType returns the engine recorded in DB_TYPE, with the same
// diagnostics previously produced by the conn command's type detection.
func (c Credentials) ResolveType() (string, error) {
	if c.Type == "" {
		return "", fmt.Errorf("DB_TYPE secret not found. This app may have been provisioned with an older version. Please re-provision the app")
	}
	if c.Type != EnginePostgres && c.Type != EngineMySQL {
		return "", fmt.Errorf("invalid DB_TYPE value: %q (must be 'postgres' or 'mysql')", c.Type)
	}
	return c.Type, nil
}

// requireCore ensures the fields every connection needs are present.
func (c Credentials) requireCore() error {
	var missing []string
	for _, kv := range []SecretKV{
		{SecretKeyHost, c.Host},
		{SecretKeyPort, c.Port},
		{SecretKeyName, c.Name},
		{SecretKeyUser, c.User},
		{SecretKeyPassword, c.Password},
	} {
		if kv.Value == "" {
			missing = append(missing, kv.Key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("required secrets not found: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ValidateContract checks the credentials satisfy the full secret contract
// for the given engine: core fields present, and schema presence consistent
// with the engine.
func (c Credentials) ValidateContract(dbType string) error {
	if err := c.requireCore(); err != nil {
		return err
	}

	hasSchema := c.Schema != ""

	if dbType == EnginePostgres && !hasSchema {
		return fmt.Errorf("DB_SCHEMA not found but DB_TYPE is 'postgres'. This indicates a database compatibility issue")
	}
	if dbType == EngineMySQL && hasSchema {
		return fmt.Errorf("DB_SCHEMA found but DB_TYPE is 'mysql'. This indicates a database compatibility issue")
	}

	return nil
}
