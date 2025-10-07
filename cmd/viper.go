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

	if cfg.DatabaseHostname == "" {
		missing = append(missing, "database_hostname")
	}

	if cfg.DatabasePassword == "" {
		missing = append(missing, "database_password")
	}

	if cfg.DatabasePort == 0 {
		missing = append(missing, "database_port")
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
