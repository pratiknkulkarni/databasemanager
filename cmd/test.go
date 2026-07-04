package cmd

import (
	"fmt"

	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/database"
	"github.com/spf13/cobra"
)

func newTestCmd(container *app.Container) *cobra.Command {
	var env string

	cmd := &cobra.Command{
		Use:   "test [app-name]",
		Short: "Test connection using credentials stored in Infisical",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			ctx := cmd.Context()

			if container.Infisical == nil {
				return fmt.Errorf("infisical client not initialized")
			}

			creds, err := fetchAppCredentials(container.Infisical, container.Config, appName, env)
			if err != nil {
				return fmt.Errorf("failed to fetch secrets: %w", err)
			}

			dbType, err := creds.ResolveType()
			if err != nil {
				return err
			}

			container.Logger.Info("testing connection", "app", appName, "type", dbType, "env", env)

			if err := database.TestAppConnection(ctx, creds); err != nil {
				return fmt.Errorf("connection test failed: %w", err)
			}

			container.Logger.Info("connection successful!")
			return nil
		},
	}

	cmd.Flags().StringVar(&env, "env", "dev", "Infisical environment")

	return cmd
}
