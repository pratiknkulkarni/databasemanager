package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// DatabaseConfig holds the configuration for the database. Includes PostgreSQL and MySQL.
// Keeping the same format as the databases increase.
type DatabaseConfig struct {
	DatabaseHostname string `mapstructure:"database_hostname"`
	DatabasePort     int    `mapstructure:"database_port"`
	DatabaseUser     string `mapstructure:"database_user"`
	DatabasePassword string `mapstructure:"database_password"`
	DatabaseName     string `mapstructure:"database_name"`

	// DatabaseSSLMode controls transport encryption for the admin dial.
	// Empty means "secure default" (resolved per engine); set "disable" to opt
	// out for local development. Postgres accepts libpq sslmode values
	// (disable|require|verify-ca|verify-full); MySQL maps them onto the
	// go-sql-driver tls values.
	DatabaseSSLMode string `mapstructure:"database_sslmode"`

	// DatabaseUserHost is the host part of the account MySQL provisions
	// (`user'@'<host>`). Empty defaults to '%'. Scope it (e.g. "10.0.%") to stop
	// provisioned accounts being reachable from anywhere. Ignored by Postgres.
	DatabaseUserHost string `mapstructure:"database_user_host"`
}

// Config holds the entire configuration including database and Infisical. including database and Infisica
type Config struct {
	Postgres DatabaseConfig `mapstructure:"postgres"`
	MySQL    DatabaseConfig `mapstructure:"mysql"`

	InfisicalProjectID    string `mapstructure:"infisical_project_id"`
	InfisicalClientID     string `mapstructure:"infisical_client_id"`
	InfisicalClientSecret string `mapstructure:"infisical_client_secret"`
	InfisicalSiteURL      string `mapstructure:"infisical_site_url"`
}

// Load reads the configuration from file, environment, and defaults.
func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to find home directory: %w", err)
		}
		v.AddConfigPath(home)
		v.AddConfigPath(".") // Look in current directory too, start with it
		v.SetConfigName(".databasemanager")
		v.SetConfigType("yaml")
	}

	v.SetEnvPrefix("DATABASEMANAGER")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	// AutomaticEnv only overrides keys Viper already knows about; keys absent
	// from the config file must be bound explicitly to be settable via
	// DATABASEMANAGER_* environment variables.
	for _, key := range envBoundKeys() {
		_ = v.BindEnv(key)
	}

	if err := v.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// Engine returns the configuration section for the named engine and whether
// the name is one the configuration schema knows about. An unconfigured but
// known engine returns a zero DatabaseConfig and true; database.New reports
// the missing section.
func (c *Config) Engine(name string) (DatabaseConfig, bool) {
	switch name {
	case "postgres":
		return c.Postgres, true
	case "mysql":
		return c.MySQL, true
	default:
		return DatabaseConfig{}, false
	}
}

// Validate ensures all required fields are present.
func (c *Config) Validate() error {
	var missing []string

	if c.InfisicalProjectID == "" {
		missing = append(missing, "infisical_project_id")
	}
	if c.InfisicalClientID == "" {
		missing = append(missing, "infisical_client_id")
	}
	if c.InfisicalClientSecret == "" {
		missing = append(missing, "infisical_client_secret")
	}

	// A config with no engine section is valid: list/conn/test only need
	// Infisical. Commands that need an engine get a clear error from
	// database.New when that engine's section is absent.
	postgresSet := c.Postgres.DatabaseHostname != ""
	mysqlSet := c.MySQL.DatabaseHostname != ""

	if postgresSet {
		if err := validateDBConfig("postgres", c.Postgres); err != nil {
			return err
		}
	}
	if mysqlSet {
		if err := validateDBConfig("mysql", c.MySQL); err != nil {
			return err
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration fields: %s", strings.Join(missing, ", "))
	}

	return nil
}

// envBoundKeys is the full set of configuration keys; it is the contract for
// what can be supplied via DATABASEMANAGER_* environment variables.
func envBoundKeys() []string {
	keys := []string{
		"infisical_project_id",
		"infisical_client_id",
		"infisical_client_secret",
		"infisical_site_url",
	}
	for _, engine := range []string{"postgres", "mysql"} {
		for _, field := range []string{"database_hostname", "database_port", "database_user", "database_password", "database_name", "database_sslmode", "database_user_host"} {
			keys = append(keys, engine+"."+field)
		}
	}
	return keys
}

func validateDBConfig(engine string, db DatabaseConfig) error {
	var missing []string
	if db.DatabaseHostname == "" {
		missing = append(missing, fmt.Sprintf("%s.database_hostname", engine))
	}
	if db.DatabasePort == 0 {
		missing = append(missing, fmt.Sprintf("%s.database_port", engine))
	}
	if db.DatabaseUser == "" {
		missing = append(missing, fmt.Sprintf("%s.database_user", engine))
	}
	if db.DatabasePassword == "" {
		missing = append(missing, fmt.Sprintf("%s.database_password", engine))
	}
	if db.DatabaseName == "" {
		missing = append(missing, fmt.Sprintf("%s.database_name", engine))
	}

	if len(missing) > 0 {
		return fmt.Errorf("incomplete %s configuration: %s", engine, strings.Join(missing, ", "))
	}
	return nil
}
