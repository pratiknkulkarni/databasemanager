package config

import "fmt"

type DatabaseConfig struct {
	DatabaseHostname string `mapstructure:"database_hostname"`
	DatabasePort     int    `mapstructure:"database_port"`
	DatabaseUser     string `mapstructure:"database_user"`
	DatabasePassword string `mapstructure:"database_password"`
	DatabaseName     string `mapstructure:"database_name"`
}

type InfisicalConfig struct {
	InfisicalProjectId    string `mapstructure:"infisical_project_id"`
	InfisicalClientId     string `mapstructure:"infisical_client_id"`
	InfisicalClientSecret string `mapstructure:"infisical_client_secret"`
	InfisicalSiteURL      string `mapstructure:"infisical_site_url"`
}

type Config struct {
	Postgres              DatabaseConfig `mapstructure:"postgres"`
	MySQL                 DatabaseConfig `mapstructure:"mysql"`
	InfisicalProjectId    string         `mapstructure:"infisical_project_id"`
	InfisicalClientId     string         `mapstructure:"infisical_client_id"`
	InfisicalClientSecret string         `mapstructure:"infisical_client_secret"`
	InfisicalSiteURL      string         `mapstructure:"infisical_site_url"`
}

func (c *Config) GetDatabaseConfig(dbType string) (*DatabaseConfig, error) {
	switch dbType {
	case "postgres":
		if c.Postgres.DatabaseHostname == "" {
			return nil, fmt.Errorf("postgres configuration not found")
		}
		return &c.Postgres, nil

	case "mysql":
		if c.MySQL.DatabaseHostname == "" {
			return nil, fmt.Errorf("mysql configuration not found")
		}
		return &c.MySQL, nil

	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}
}

//func NewConfig(infisicalConfig InfisicalConfig, databaseConfig DatabaseConfig) *Config {
//	return &Config{DatabaseConfig: databaseConfig, InfisicalConfig: infisicalConfig}
//}
