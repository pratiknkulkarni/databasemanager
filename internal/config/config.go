package config

// DatabaseConfig holds the configuration for the database. Includes PostgreSQL and MySQL.
// Keeping the same format as the databases increase.
type DatabaseConfig struct {
	DatabaseHostname string `mapstructure:"database_hostname"`
	DatabasePort     int    `mapstructure:"database_port"`
	DatabaseUser     string `mapstructure:"database_user"`
	DatabasePassword string `mapstructure:"database_password"`
	DatabaseName     string `mapstructure:"database_name"`
}

// InfisicalConfig holds the configuration details for Infisical.
// Deprecated: I am directly putting these in the Config struct
type InfisicalConfig struct {
	InfisicalProjectId    string `mapstructure:"infisical_project_id"`
	InfisicalClientId     string `mapstructure:"infisical_client_id"`
	InfisicalClientSecret string `mapstructure:"infisical_client_secret"`
	InfisicalSiteURL      string `mapstructure:"infisical_site_url"`
}

// Config holds the entire configuration including database and Infisical. including database and Infisica
type Config struct {
	Postgres DatabaseConfig `mapstructure:"postgres"`
	MySQL    DatabaseConfig `mapstructure:"mysql"`

	InfisicalProjectId    string `mapstructure:"infisical_project_id"`
	InfisicalClientId     string `mapstructure:"infisical_client_id"`
	InfisicalClientSecret string `mapstructure:"infisical_client_secret"`
	InfisicalSiteURL      string `mapstructure:"infisical_site_url"`
}
