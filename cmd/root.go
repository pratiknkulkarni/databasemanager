package cmd

import (
	"context"
	"fmt"
	"os"

	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// configKey is used to store the configuration in the context
type configKey struct{}

// infisicalClientKey is used to pass the infisical client as a context
type infisicalClientKey struct{}

// databaseClientKey is used to pass the infisical client as a context
type databaseClientKey struct{}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "databasemanager",
	Short: "a simple databasemanager to provision database and save details in Infisical Secrets Manager",
	//TODO: I'll have to add even mysql
	Long: `databasemanager is a CLI tool for provisioning isolated PostgreSQL databases 
and securely managing their credentials in Infisical Secrets Manager.

Each provisioned application gets:
  - A dedicated PostgreSQL database
  - An isolated database user with restricted permissions
  - A private schema with configured search path
  - All credentials securely stored in Infisical

Common Operations:
  - Provision: Create a new isolated database with automatic secret storage
  - List: View all provisioned apps or secrets for a specific app
  - Delete: Remove databases, users, and secrets completely
  - Test: Verify database and Infisical connectivity
  - Get Connection String: Generate connection strings in various formats (uri, psql)

Examples:
  # Provision a new app database
  databasemanager provision myapp

  # List all provisioned apps
  databasemanager list

  # List secrets for a specific app
  databasemanager list myapp

  # Get connection string
  databasemanager getconnstring myapp
  
  # Delete an app (with confirmation)
  databasemanager delete myapp

  # Test connections
  databasemanager test --app myapp

Configuration:
  Configuration can be provided via:
    - Config file: .databasemanager.yaml (searched in current dir and home dir)
    - Environment variables (prefixed with DATABASEMANAGER_)
    - Command-line flags

  Required settings:
    - database-hostname: PostgreSQL server hostname
    - database-port: PostgreSQL server port (default: 5432)
    - database-password: Admin user password
    - infisical-project-id: Infisical project ID
    - infisical-client-id: Infisical client ID
    - infisical-client-secret: Infisical client secret
    - infisical-site-url: Infisical server URL
`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		initializeViper()
		if err := initConfig(); err != nil {
			return err
		}

		infisicalClient, err := initInfisicalClient(&cfg)
		if err != nil {
			return fmt.Errorf("failed to initialize infisical client: %w", err)
		}

		databaseClient, err := initDatabaseClient(&cfg, cmd)
		if err != nil {
			return fmt.Errorf("failed to initialize database client: %w", err)
		}

		// attach contexts together instead of one by one
		ctx := context.WithValue(cmd.Context(), configKey{}, &cfg)
		ctx = context.WithValue(ctx, infisicalClientKey{}, infisicalClient)
		ctx = context.WithValue(ctx, databaseClientKey{}, databaseClient)

		cmd.SetContext(ctx)

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initializeViper)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default searches for .databasemanager.yaml in home directory and current directory)")

	rootCmd.PersistentFlags().String("database-hostname", "localhost", "DatabaseName hostname")
	rootCmd.PersistentFlags().Int("database-port", 5432, "DatabaseName port")
	rootCmd.PersistentFlags().String("database-password", "", "DatabaseName password")
	rootCmd.PersistentFlags().String("infisical-project-id", "", "Infisical project ID")
	rootCmd.PersistentFlags().String("infisical-client-id", "", "Infisical client ID")
	rootCmd.PersistentFlags().String("infisical-client-secret", "", "Infisical client secret")
	rootCmd.PersistentFlags().String("infisical-site-url", "", "Infisical site URL")
	rootCmd.PersistentFlags().String("database", "postgres", "DatabaseName type")

	viper.BindPFlag("database_hostname", rootCmd.PersistentFlags().Lookup("database-hostname"))
	viper.BindPFlag("database_port", rootCmd.PersistentFlags().Lookup("database-port"))
	viper.BindPFlag("database_password", rootCmd.PersistentFlags().Lookup("database-password"))
	viper.BindPFlag("infisical_project_id", rootCmd.PersistentFlags().Lookup("infisical-project-id"))
	viper.BindPFlag("infisical_client_id", rootCmd.PersistentFlags().Lookup("infisical-client-id"))
	viper.BindPFlag("infisical_client_secret", rootCmd.PersistentFlags().Lookup("infisical-client-secret"))
	viper.BindPFlag("infisical_site_url", rootCmd.PersistentFlags().Lookup("infisical-site-url"))

	//rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func initInfisicalClient(cfg *config.Config) (infisical.InfisicalClientInterface, error) {
	client := infisical.NewInfisicalClient(context.Background(), infisical.Config{
		SiteUrl:              cfg.InfisicalSiteURL,
		AutoTokenRefresh:     true,
		CacheExpiryInSeconds: 0,
	})

	_, err := client.Auth().UniversalAuthLogin(cfg.InfisicalClientId, cfg.InfisicalClientSecret)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	return client, nil
}

func initDatabaseClient(cfg *config.Config, cmd *cobra.Command) (database.Database, error) {
	// init the client here, authentication and all
	// add a switch case here to check type of the database - sqlite/postgres/etc
	dbType, err := cmd.Flags().GetString("database")

	if err != nil {
		return nil, fmt.Errorf("failed to get database flag: %w", err)
	}

	// defaulting to postgres
	// maybe have an option to default?
	if dbType == "" {
		dbType = "postgres"
	}

	//TODO: keep these in a slice and iterate through them. Might add more databases later on
	if dbType != "postgres" && dbType != "mysql" {
		return nil, fmt.Errorf("invalid database type %q, must be: postgres, mysql", dbType)
	}

	var databaseClient database.Database
	switch dbType {
	case "postgres":
		postgresClient, err := database.NewPostgresClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create postgres client: %w", err)
		}
		databaseClient = postgresClient

	case "mysql":
		mysqlClient, err := database.NewMySQLClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create mysql client: %w", err)
		}
		databaseClient = mysqlClient

	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}

	ctx := cmd.Context()
	err = databaseClient.Connect(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s database: %w", dbType, err)
	}

	return databaseClient, nil
	//return database.NewPostgresClient(cfg)
}

func GetConfig(cmd *cobra.Command) *config.Config {
	return cmd.Context().Value(configKey{}).(*config.Config)
}

//func GetInfisicalClient(cmd *cobra.Command) *infisical.InfisicalClient {
//	client := cmd.Context().Value(infisicalClientKey{}).(*infisical.InfisicalClient)
//	return client
//}

func GetInfisicalClient(cmd *cobra.Command) infisical.InfisicalClientInterface {
	return cmd.Context().Value(infisicalClientKey{}).(infisical.InfisicalClientInterface)
}

func GetDatabaseClient(cmd *cobra.Command) (database.Database, error) {
	return cmd.Context().Value(databaseClientKey{}).(database.Database), nil
}
