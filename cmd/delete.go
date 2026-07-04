package cmd

import (
	"bufio"
	"fmt"
	"strings"

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

			// Resolve what was actually provisioned from Infisical — the
			// recorded names, not guesses derived from the app name.
			resolver := services.NewProvisioner(services.ProvisionerDeps{
				Secrets:   container.Infisical,
				Logger:    container.Logger,
				ProjectID: container.Config.InfisicalProjectID,
			})
			creds, err := resolver.ResolveApp(appName, env)
			if err != nil {
				return err
			}

			engine := creds.Type
			if engine == "" {
				// Legacy apps provisioned before DB_TYPE existed.
				if dbType == "" {
					return fmt.Errorf("cannot determine database type for app %q: DB_TYPE secret is missing; pass --type", appName)
				}
				engine = dbType
			} else if dbType != "" && dbType != engine {
				return fmt.Errorf("DB_TYPE mismatch: stored type is '%s' but --type flag specifies '%s'", engine, dbType)
			}

			engineCfg, _ := container.Config.Engine(engine)
			db, err := database.New(engine, engineCfg)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()

			if !force {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"About to delete database %q and user %q for app %q (environment %q).\nType 'y' or 'yes' to confirm: ",
					creds.Name, creds.User, appName, env)
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				answer := strings.ToLower(strings.TrimSpace(line))
				if answer != "y" && answer != "yes" {
					container.Logger.Info("deletion aborted", "app", appName)
					return nil
				}
			}

			provisioner := services.NewProvisioner(services.ProvisionerDeps{
				DB:        db,
				Secrets:   container.Infisical,
				Logger:    container.Logger,
				ProjectID: container.Config.InfisicalProjectID,
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
