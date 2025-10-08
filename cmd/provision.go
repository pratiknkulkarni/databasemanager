package cmd

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/spf13/cobra"

	"github.com/praaatik/databasemanager/internal/database"
)

// TODO: hardcoding the appName and appPassword for now. I need to get this from the command line
var (
	appName     string
	appPassword string
	dbName      string
	userName    string
	schemaName  string
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
		if appName == "" {
			return fmt.Errorf("--app are required")
		}

		cfg := GetConfig(cmd)

		// Init Postgres client
		client, err := database.NewPostgresClient(cfg)
		if err != nil {
			return fmt.Errorf("failed to create postgres client: %w", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// TODO: generate random password
		if appPassword == "" {
			appPassword = "supersecret123"
			log.Printf("Generated random password for app=%s", appName)
		}

		provisionOptions := database.ProvisionOptions{
			AppName:     appName,
			AppPassword: appPassword,
			Database:    dbName,
			User:        userName,
			Schema:      schemaName,
		}

		log.Printf("Provisioning database for app=%s ...", appName)
		if err := client.Provision(ctx, provisionOptions); err != nil {
			return fmt.Errorf("failed to provision database: %w", err)
		}

		log.Printf("Provisioned database for app=%s", appName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(provisionCmd)

	provisionCmd.Flags().StringVar(&appName, "app", "", "Application name (required)")
	provisionCmd.Flags().StringVar(&appPassword, "password", "", "Application database password (optional, random if empty)")
	provisionCmd.Flags().StringVar(&dbName, "dbname", "", "Override default generated database name (optional)")
	provisionCmd.Flags().StringVar(&userName, "user", "", "Override default generated database user (optional)")
	provisionCmd.Flags().StringVar(&schemaName, "schema", "", "Override default generated schema name (optional)")

	_ = provisionCmd.MarkFlagRequired("app")
}
