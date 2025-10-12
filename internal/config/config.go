package config

type DatabaseConfig struct {
	DatabaseHostname string `mapstructure:"database_hostname"`
	DatabasePort     int    `mapstructure:"database_port"`
	DatabaseUser     string `mapstructure:"database_user"`
	DatabasePassword string `mapstructure:"database_password"`
}

type InfisicalConfig struct {
	InfisicalProjectId    string `mapstructure:"infisical_project_id"`
	InfisicalClientId     string `mapstructure:"infisical_client_id"`
	InfisicalClientSecret string `mapstructure:"infisical_client_secret"`
	InfisicalSiteURL      string `mapstructure:"infisical_site_url"`
}

type Config struct {
	DatabaseConfig  `mapstructure:",squash"`
	InfisicalConfig `mapstructure:",squash"`
}

func NewConfig(infisicalConfig InfisicalConfig, databaseConfig DatabaseConfig) *Config {
	return &Config{DatabaseConfig: databaseConfig, InfisicalConfig: infisicalConfig}
}
