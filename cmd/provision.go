package cmd

import (
	"fmt"

	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/database"
	"github.com/praaatik/databasemanager/internal/services"
	"github.com/spf13/cobra"
)

func newProvisionCmd(container *app.Container) *cobra.Command {
	var (
		dbType string
		dbUser string
		dbPass string
		dbName string
		env    string
	)

	cmd := &cobra.Command{
		Use:   "provision [app-name]",
		Short: "Provision a new database and sync secrets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			ctx := cmd.Context()

			switch dbType {
			case "postgres":
				client, err := database.NewPostgresClient(container.Config)
				if err != nil {
					return err
				}
				container.DB = client
			case "mysql":
				client, err := database.NewMySQLClient(container.Config)
				if err != nil {
					return err
				}
				container.DB = client
			default:
				return fmt.Errorf("unsupported database type: %s", dbType)
			}

			provisioner := services.NewProvisioner(container)

			req := services.ProvisionRequest{
				AppName:     appName,
				Type:        dbType,
				Environment: env,
				DBName:      dbName,
				DBUser:      dbUser,
				DBPassword:  dbPass,
			}

			result, err := provisioner.Run(ctx, req)
			if err != nil {
				return err
			}

			container.Logger.Info("Successfully provisioned",
				"app", appName,
				"db_name", result.DatabaseName,
				"connection", "Secrets synced to Infisical")

			return nil
		},
	}

	cmd.Flags().StringVar(&dbType, "type", "postgres", "Database type (postgres|mysql)")
	cmd.Flags().StringVar(&env, "env", "dev", "Target environment (dev|staging|prod)")
	cmd.Flags().StringVar(&dbUser, "user", "", "Override database user (default: auto-generated)")
	cmd.Flags().StringVar(&dbPass, "pass", "", "Override database password (default: secure random)")
	cmd.Flags().StringVar(&dbName, "db", "", "Override database name (default: auto-generated)")

	return cmd
}
