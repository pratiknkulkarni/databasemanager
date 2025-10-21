package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/charmbracelet/log"
	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
	"github.com/praaatik/databasemanager/internal/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	verbosity int
	logFile   string
	logr      *log.Logger
)

// configKey is used to store the configuration in the context
type configKey struct{}

// infisicalClientKey is used to pass the infisical client as a context
type infisicalClientKey struct{}

// databaseClientKey is used to pass the infisical client as a context
type databaseClientKey struct{}

type loggerKey struct{}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "databasemanager",
	Short: "a simple databasemanager to provision database and save details in Infisical Secrets Manager",
	//TODO: I'll have to add even mysql
	Long: `databasemanager is a CLI tool for provisioning isolated PostgreSQL and MySQL databases 
and securely managing their credentials in Infisical Secrets Manager.

Each provisioned application gets:
  - A dedicated PostgreSQL/MySQL database
  - An isolated database user with restricted permissions
  - A private schema with configured search path (for PostgreSQL)
  - All credentials securely stored in Infisical

Common Operations:
  - Provision: Create a new isolated database with automatic secret storage
  - List: View all provisioned apps or secrets for a specific app
  - Delete: Remove databases, users, and secrets completely
  - Test: Verify database and Infisical connectivity
  - Get Connection String: Generate connection strings in various formats (uri, psql, mysql, env)

Examples:
  # Provision a new app database
  databasemanager provision myapp

  # List all provisioned apps
  databasemanager list

  # List secrets for a specific app
  databasemanager list myapp

  # Get connection string
  databasemanager conn myapp
  
  # Delete an app
  databasemanager delete myapp

  # Test connections
  databasemanager test --app myapp

Configuration:
  Configuration can be provided via:
    - Config file: .databasemanager.yaml OR .databasemanager.toml (searched in current dir and home dir)
    - Environment variables (prefixed with DATABASEMANAGER_)
    - Command-line flags

  Required settings:
    - database-hostname: Database server hostname
    - database-port: Database server port
    - database-password: Database admin user password
    - infisical-project-id: Infisical project ID
    - infisical-client-id: Infisical client ID
    - infisical-client-secret: Infisical client secret
    - infisical-site-url: Infisical server URL
`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		logLevel := log.InfoLevel

		switch verbosity {
		case 0:
			logLevel = log.WarnLevel
		case 1:
			logLevel = log.InfoLevel
		case 2:
			logLevel = log.DebugLevel
		default:
			logLevel = log.DebugLevel
		}

		logr = logger.New(logger.LogConfig{
			Level:   logLevel,
			LogFile: logFile,
		})

		initializeViper()
		if err := initConfig(); err != nil {
			return err
		}

		infisicalClient, err := initInfisicalClient(&cfg)
		if err != nil {
			logr.Fatalf("failed to initialize infisical client: %v", err)
			return fmt.Errorf("failed to initialize infisical client: %w", err)
		}

		databaseClient, err := initDatabaseClient(&cfg, cmd)
		if err != nil {
			logr.Fatalf("failed to initialize database client: %v", err)
			return fmt.Errorf("failed to initialize database client: %w", err)
		}

		// attach contexts together instead of one by one
		ctx := context.WithValue(cmd.Context(), configKey{}, &cfg)
		ctx = context.WithValue(ctx, infisicalClientKey{}, infisicalClient)
		ctx = context.WithValue(ctx, databaseClientKey{}, databaseClient)
		ctx = context.WithValue(ctx, loggerKey{}, logr)

		cmd.SetContext(ctx)

		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		logr.Fatal("Unable to Execute", err)
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
	rootCmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", "Verbosity (-v, -vv)")

	viper.BindPFlag("database_hostname", rootCmd.PersistentFlags().Lookup("database-hostname"))
	viper.BindPFlag("database_port", rootCmd.PersistentFlags().Lookup("database-port"))
	viper.BindPFlag("database_password", rootCmd.PersistentFlags().Lookup("database-password"))
	viper.BindPFlag("infisical_project_id", rootCmd.PersistentFlags().Lookup("infisical-project-id"))
	viper.BindPFlag("infisical_client_id", rootCmd.PersistentFlags().Lookup("infisical-client-id"))
	viper.BindPFlag("infisical_client_secret", rootCmd.PersistentFlags().Lookup("infisical-client-secret"))
	viper.BindPFlag("infisical_site_url", rootCmd.PersistentFlags().Lookup("infisical-site-url"))
}

func initInfisicalClient(cfg *config.Config) (infisical.InfisicalClientInterface, error) {
	client := infisical.NewInfisicalClient(context.Background(), infisical.Config{
		SiteUrl:              cfg.InfisicalSiteURL,
		AutoTokenRefresh:     true,
		CacheExpiryInSeconds: 0,
	})

	_, err := client.Auth().UniversalAuthLogin(cfg.InfisicalClientId, cfg.InfisicalClientSecret)
	logr.Debug("Infisical authentication success")

	if err != nil {
		logr.Fatal("Infisical authentication failed")
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	return client, nil
}

func initDatabaseClient(cfg *config.Config, cmd *cobra.Command) (database.Database, error) {
	dbType, err := cmd.Flags().GetString("database")

	if err != nil {
		logr.Errorf("failed to get database flag: %v", err)
		return nil, fmt.Errorf("failed to get database flag: %v", err)
	}

	// defaulting to postgres
	// maybe have an option to default?
	if dbType == "" {
		dbType = "postgres"
	}

	//TODO: keep these in a slice and iterate through them. Might add more databases later on
	if dbType != "postgres" && dbType != "mysql" {
		logr.Errorf("invalid database type %q, must be: postgres, mysql", dbType)
		return nil, fmt.Errorf("invalid database type %q, must be: postgres, mysql", dbType)
	}

	var databaseClient database.Database
	switch dbType {
	case "postgres":
		postgresClient, err := database.NewPostgresClient(cfg)
		if err != nil {
			logr.Errorf("failed to create postgres client: %v", err)
			return nil, fmt.Errorf("failed to create postgres client: %w", err)
		}
		databaseClient = postgresClient

	case "mysql":
		mysqlClient, err := database.NewMySQLClient(cfg)
		if err != nil {
			logr.Errorf("failed to create mysql client: %v", err)
			return nil, fmt.Errorf("failed to create mysql client: %w", err)
		}
		databaseClient = mysqlClient

	default:
		logr.Fatalf("unsupported database type: %s", dbType)
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}

	log.Debugf("database type set to %s\n", dbType)

	ctx := cmd.Context()
	err = databaseClient.Connect(ctx)

	if err != nil {
		logr.Fatalf("failed to connect to %s database: %v", dbType, err)
		return nil, fmt.Errorf("failed to connect to %s database: %w", dbType, err)
	}

	logr.Infof("connection to %s database success", dbType)
	return databaseClient, nil
}

func GetConfig(cmd *cobra.Command) *config.Config {
	return cmd.Context().Value(configKey{}).(*config.Config)
}

func GetInfisicalClient(cmd *cobra.Command) (infisical.InfisicalClientInterface, error) {
	v := cmd.Context().Value(infisicalClientKey{})
	if v == nil {
		return nil, fmt.Errorf("infisical client not present in the context")
	}

	client, ok := v.(infisical.InfisicalClientInterface)
	if !ok {
		return nil, fmt.Errorf("infisical client has unexpected type")
	}

	return client, nil
}

func GetDatabaseClient(cmd *cobra.Command) database.Database {
	return cmd.Context().Value(databaseClientKey{}).(database.Database)
}

func GetLogger(cmd *cobra.Command) (*log.Logger, error) {
	return cmd.Context().Value(loggerKey{}).(*log.Logger), nil
}
