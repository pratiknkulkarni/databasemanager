package config

import (
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

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
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

	postgresSet := c.Postgres.DatabaseHostname != ""
	mysqlSet := c.MySQL.DatabaseHostname != ""

	//TODO: maybe I throw in an exception here? Panic?
	if !postgresSet && !mysqlSet {
	}

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
