package cmd

import (
	"fmt"

	"github.com/pratiknkulkarni/databasemanager/internal/app"
	"github.com/pratiknkulkarni/databasemanager/internal/database"
	"github.com/pratiknkulkarni/databasemanager/internal/services"
	"github.com/spf13/cobra"
)

func newRotateCmd(container *app.Container) *cobra.Command {
	var (
		dbType   string
		env      string
		pass     string
		force    bool
		noVerify bool
	)

	cmd := &cobra.Command{
		Use:   "rotate [app-name]",
		Short: "Rotate an app's database password and update Infisical",
		Long: `Rotate issues a new password for the app's database user, applies it to the
database, and updates the DB_PASSWORD secret in Infisical.

The database changes first; if the Infisical update then fails, the previous
password is restored so a failed rotation leaves nothing changed. Existing
database sessions are unaffected — passwords are checked at connection time —
but any app that reconnects must fetch the new secret.

Rotation also serves as password recovery: if DB_PASSWORD was lost or deleted,
rotate issues a fresh one and records it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			ctx := cmd.Context()

			if container.Infisical == nil {
				return fmt.Errorf("infisical client not initialized")
			}
			store := services.NewSecretStore(container.Infisical, container.Config.InfisicalProjectID)

			// Resolve identity from Infisical — the source of truth for the
			// recorded user name, mirroring delete.
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
					"About to rotate the password for user %q (app %q, database %q, host %q, environment %q).\nApps holding the current password will fail to authenticate on their next (re)connect.\nType 'y' or 'yes' to confirm: ",
					creds.User, appName, creds.Name, creds.Host, env)
				if !confirmAction(cmd, prompt) {
					container.Logger.Info("rotation aborted", "app", appName)
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

			result, err := provisioner.Rotate(ctx, appName, env, pass)
			if err != nil {
				if result != nil && !result.RolledBack {
					// The new password is live on the database but recorded
					// nowhere — this terminal output is the only copy.
					container.Logger.Warn("record this credential now — it is not stored anywhere", "app", appName)
					fmt.Printf("%s=%s\n", database.SecretKeyPassword, result.Credentials.Password)
				}
				return err
			}

			container.Logger.Info("password rotated and recorded in Infisical",
				"app", appName, "user", result.Credentials.User)

			if !noVerify {
				if err := database.TestAppConnection(ctx, result.Credentials, engineCfg.DatabaseSSLMode); err != nil {
					return fmt.Errorf("rotation succeeded but verification failed (possibly a reachability issue from this machine — see --no-verify): %w", err)
				}
				container.Logger.Info("verified: app credentials connect successfully")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&dbType, "type", "", "Database type fallback for apps missing DB_TYPE (postgres|mysql)")
	cmd.Flags().StringVar(&env, "env", "dev", "Infisical environment")
	cmd.Flags().StringVar(&pass, "pass", "", "Override the new password (default: secure random)")
	cmd.Flags().BoolVar(&force, "force", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "Skip the post-rotation connectivity check")

	return cmd
}
