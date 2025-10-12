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
	Long: `A longer description that spans multiple lines and likely contains
examples and usage of using your application. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		initializeViper()
		if err := initConfig(); err != nil {
			return err
		}

		infisicalClient, err := initInfisicalClient(&cfg)
		if err != nil {
			return fmt.Errorf("failed to initialize infisical client: %w", err)
		}

		databaseClient, err := initDatabaseClient(&cfg)
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

	rootCmd.PersistentFlags().String("database-hostname", "localhost", "Database hostname")
	rootCmd.PersistentFlags().Int("database-port", 5432, "Database port")
	rootCmd.PersistentFlags().String("database-password", "", "Database password")
	rootCmd.PersistentFlags().String("infisical-project-id", "", "Infisical project ID")
	rootCmd.PersistentFlags().String("infisical-client-id", "", "Infisical client ID")
	rootCmd.PersistentFlags().String("infisical-client-secret", "", "Infisical client secret")
	rootCmd.PersistentFlags().String("infisical-site-url", "", "Infisical site URL")

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
		SiteUrl:          cfg.InfisicalSiteURL,
		AutoTokenRefresh: true,
	})

	_, err := client.Auth().UniversalAuthLogin(cfg.InfisicalClientId, cfg.InfisicalClientSecret)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	return client, nil
}

func initDatabaseClient(cfg *config.Config) (database.Database, error) {
	// init hte client here, authentication and all
	// add a switch case here to check type of the database - sqlite/postgres/etc
	return database.NewPostgresClient(cfg)
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

func GetDatabaseClient(cmd *cobra.Command) database.Database {
	return cmd.Context().Value(databaseClientKey{}).(database.Database)
}
