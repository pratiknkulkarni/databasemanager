package cmd

import (
	"fmt"

	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/database"
	"github.com/praaatik/databasemanager/internal/services"
	"github.com/spf13/cobra"
)

func newDeleteCmd(container *app.Container) *cobra.Command {
	var (
		dbType string
		env    string
		force  bool
	)

	cmd := &cobra.Command{
		Use:   "delete [app-name]",
		Short: "Deprovision a database and remove its secrets from Infisical",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			ctx := cmd.Context()

			if container.Infisical == nil {
				return fmt.Errorf("infisical client not initialized")
			}
			store := services.NewSecretStore(container.Infisical, container.Config.InfisicalProjectID)

			// Resolve what was actually provisioned from Infisical — the
			// recorded names, not guesses derived from the app name.
			resolver := services.NewProvisioner(services.ProvisionerDeps{
				Secrets: store,
				Logger:  container.Logger,
			})
			creds, err := resolver.ResolveApp(appName, env)
			if err != nil {
				return err
			}

			engine, err := resolveEngine(creds, dbType, appName)
			if err != nil {
				return err
			}

			engineCfg, _ := container.Config.Engine(engine)
			warnInsecureTLS(container, engine, engineCfg)
			db, err := database.New(engine, engineCfg)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()

			if !force {
				prompt := fmt.Sprintf(
					"About to delete database %q and user %q on host %q for app %q (environment %q).\nType 'y' or 'yes' to confirm: ",
					creds.Name, creds.User, creds.Host, appName, env)
				if !confirmAction(cmd, prompt) {
					container.Logger.Info("deletion aborted", "app", appName)
					return nil
				}
			}

			provisioner := services.NewProvisioner(services.ProvisionerDeps{
				DB:        db,
				Secrets:   store,
				Logger:    container.Logger,
				Engine:    engine,
				EngineCfg: engineCfg,
			})

			container.Logger.Info("deleting service", "app", appName, "type", engine)

			if err := provisioner.Deprovision(ctx, appName, env, creds); err != nil {
				return err
			}

			container.Logger.Info("Successfully deleted", "app", appName)
			return nil
		},
	}

	cmd.Flags().StringVar(&dbType, "type", "", "Database type fallback for apps missing DB_TYPE (postgres|mysql)")
	cmd.Flags().StringVar(&env, "env", "dev", "Infisical environment")
	cmd.Flags().BoolVar(&force, "force", false, "Skip the confirmation prompt")

	return cmd
}
