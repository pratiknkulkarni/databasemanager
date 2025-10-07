package cmd

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/spf13/cobra"

	"github.com/praaatik/databasemanager/internal/database"
)

var (
	appName     string
	appPassword string
)

// provisionCmd represents the provision command
var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision a new isolated application database",
	Long: `Provision a new database, roles, and schema for an application.
This will create:

* A dedicated database
* Owner, read-write, and read-only roles
* Application login role with password
* An isolated schema following least-privilege principles`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if appName == "" || appPassword == "" {
			return fmt.Errorf("--app and --password are required")
		}

		//infisicalClient := GetInfisicalClient(cmd)
		//dbClient := GetDatabaseClient(cmd)
		cfg := GetConfig(cmd)
		// Init Postgres client
		client, err := database.NewPostgresClient(cfg)
		if err != nil {
			return fmt.Errorf("failed to create postgres client: %w", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		log.Printf("Provisioning database for app=%s ...", appName)
		if err := client.Provision(ctx, appName, appPassword); err != nil {
			return fmt.Errorf("failed to provision database: %w", err)
		}

		log.Printf("Provisioned database for app=%s", appName)
		return nil

	},
}

func init() {
	rootCmd.AddCommand(provisionCmd)

	provisionCmd.Flags().StringVar(&appName, "app", "", "Application name (required)")
	provisionCmd.Flags().StringVar(&appPassword, "password", "", "Application database password (required)")

	_ = provisionCmd.MarkFlagRequired("app")
	_ = provisionCmd.MarkFlagRequired("password")
}
