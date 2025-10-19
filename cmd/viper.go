package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/praaatik/databasemanager/internal/config"
	"github.com/spf13/viper"
)

var cfg config.Config
var cfgFile string

func initializeViper() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting home directory: %v\n", err)
			os.Exit(1)
		}
		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.SetConfigName(".databasemanager")
	}

	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			fmt.Println("No config file found")
		} else {
			fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}

	viper.AutomaticEnv()
	viper.SetEnvPrefix("DATABASEMANAGER")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
}

func initConfig() error {
	if err := viper.Unmarshal(&cfg); err != nil {
		return fmt.Errorf("unable to decode configuration: %w", err)
	}

	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	return nil
}

func validateConfig(cfg config.Config) error {
	var missing []string

	//if cfg.AppName == "" {
	//	missing = append(missing, "database_hostname")
	//}
	//
	//if cfg.DatabasePassword == "" {
	//	missing = append(missing, "database_password")
	//}
	//
	//if cfg.DatabasePort == 0 {
	//	missing = append(missing, "database_port")
	//}

	hasPostgres := cfg.Postgres.DatabaseHostname != "" && cfg.Postgres.DatabasePort != 0
	hasMySQL := cfg.MySQL.DatabaseHostname != "" && cfg.MySQL.DatabasePort != 0

	if !hasPostgres && !hasMySQL {
		return fmt.Errorf("at least one database (postgres or mysql) must be configured")
	}

	if hasPostgres {
		if err := validateDatabaseConfig("postgres", cfg.Postgres); err != nil {
			return err
		}
	}

	if hasMySQL {
		if err := validateDatabaseConfig("mysql", cfg.MySQL); err != nil {
			return err
		}
	}

	if cfg.InfisicalProjectId == "" {
		missing = append(missing, "infisical_project_id")
	}
	if cfg.InfisicalClientId == "" {
		missing = append(missing, "infisical_client_id")
	}
	if cfg.InfisicalClientSecret == "" {
		missing = append(missing, "infisical_client_secret")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration fields: %s", strings.Join(missing, ", "))
	}

	return nil
}

func validateDatabaseConfig(dbType string, dbCfg config.DatabaseConfig) error {
	var missing []string

	if dbCfg.DatabaseHostname == "" {
		missing = append(missing, fmt.Sprintf("%s.database_hostname", dbType))
	}
	if dbCfg.DatabasePort == 0 {
		missing = append(missing, fmt.Sprintf("%s.database_port", dbType))
	}
	if dbCfg.DatabaseUser == "" {
		missing = append(missing, fmt.Sprintf("%s.database_user", dbType))
	}
	if dbCfg.DatabasePassword == "" {
		missing = append(missing, fmt.Sprintf("%s.database_password", dbType))
	}

	if len(missing) > 0 {
		return fmt.Errorf("incomplete %s configuration, missing: %s", dbType, strings.Join(missing, ", "))
	}

	return nil
}

//func GetConfig() *config.Config {
//	return &cfg
//}
