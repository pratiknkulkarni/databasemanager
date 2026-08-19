package database

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pratiknkulkarni/databasemanager/internal/config"
)

// Supported engine names. These are the values accepted by --type flags and
// recorded in the DB_TYPE secret.
const (
	EnginePostgres = "postgres"
	EngineMySQL    = "mysql"
)

// engineFactories is the single registry of supported engines. Adding an
// engine means adding an entry here and implementing Database.
var engineFactories = map[string]func(config.DatabaseConfig) Database{
	EnginePostgres: func(cfg config.DatabaseConfig) Database { return newPostgresClient(cfg) },
	EngineMySQL:    func(cfg config.DatabaseConfig) Database { return newMySQLClient(cfg) },
}

// Engines returns the supported engine names, sorted.
func Engines() []string {
	names := make([]string, 0, len(engineFactories))
	for name := range engineFactories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// New constructs the adapter for the given engine using that engine's own
// configuration section. It fails fast when the engine is unknown or its
// section is absent from the configuration.
func New(engine string, cfg config.DatabaseConfig) (Database, error) {
	factory, ok := engineFactories[engine]
	if !ok {
		return nil, fmt.Errorf("unsupported database type: %s (supported: %s)", engine, strings.Join(Engines(), ", "))
	}
	if cfg.DatabaseHostname == "" {
		return nil, fmt.Errorf("%s is not configured: set the %s section in the configuration file", engine, engine)
	}
	return factory(cfg), nil
}
