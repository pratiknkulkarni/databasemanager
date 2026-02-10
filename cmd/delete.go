package cmd

import (
	"fmt"

	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/database"
	"github.com/spf13/cobra"
)

func newDeleteCmd(container *app.Container) *cobra.Command {
	var dbType string

	cmd := &cobra.Command{
		Use:   "delete [app-name]",
		Short: "Deprovision a database and user",
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

			dbName := fmt.Sprintf("%s_db", appName)
			dbUser := fmt.Sprintf("%s_user", appName)

			container.Logger.Info("deleting service", "app", appName, "type", dbType)

			if err := container.DB.Delete(ctx, dbName, dbUser); err != nil {
				return fmt.Errorf("failed to delete database: %w", err)
			}

			container.Logger.Info("Successfully deleted", "app", appName)
			return nil
		},
	}

	cmd.Flags().StringVar(&dbType, "type", "postgres", "Database type (postgres|mysql)")
	return cmd
}
