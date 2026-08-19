package cmd

import (
	"fmt"

	"github.com/pratiknkulkarni/databasemanager/internal/app"
	"github.com/pratiknkulkarni/databasemanager/internal/database"
	"github.com/pratiknkulkarni/databasemanager/internal/services"
	"github.com/spf13/cobra"
)

func newProvisionCmd(container *app.Container) *cobra.Command {
	var (
		dbType   string
		dbUser   string
		dbPass   string
		dbName   string
		dbSchema string
		dbHost   string
		dbPort   int
		env      string
		adopt    bool
		force    bool
	)

	cmd := &cobra.Command{
		Use:   "provision [app-name]",
		Short: "Provision a new database and sync secrets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			ctx := cmd.Context()

			engineCfg, _ := container.Config.Engine(dbType)
			warnInsecureTLS(container, dbType, engineCfg)
			db, err := database.New(dbType, engineCfg)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()

			// Adoption mutates resources this run did not create (password
			// reset, hardening re-applied, secrets reconciled) — make the
			// operator confirm exactly that.
			if adopt && !force {
				prompt := fmt.Sprintf(
					"About to ADOPT app %q (%s, environment %q) on %s:%d.\n"+
						"Any existing database and user under this app's names will be kept, but:\n"+
						"  - the user's password will be RESET (apps holding the current password lose access on reconnect)\n"+
						"  - privilege hardening will be re-applied\n"+
						"  - recorded secrets at /%s will be reconciled in place\n"+
						"Type 'y' or 'yes' to confirm: ",
					appName, dbType, env, engineCfg.DatabaseHostname, engineCfg.DatabasePort, appName)
				if !confirmAction(cmd, prompt) {
					container.Logger.Info("adoption aborted", "app", appName)
					return nil
				}
			}

			provisioner := services.NewProvisioner(services.ProvisionerDeps{
				DB:        db,
				Secrets:   services.NewSecretStore(container.Infisical, container.Config.InfisicalProjectID),
				Logger:    container.Logger,
				Engine:    dbType,
				EngineCfg: engineCfg,
			})

			req := services.ProvisionRequest{
				AppName:     appName,
				Environment: env,
				Adopt:       adopt,
				DBName:      dbName,
				DBUser:      dbUser,
				DBPassword:  dbPass,
				DBSchema:    dbSchema,
				Host:        dbHost,
				Port:        dbPort,
			}

			result, err := provisioner.Run(ctx, req)
			if err != nil {
				if result != nil {
					// The database was created but the sync failed: this
					// terminal output is the only copy of the generated
					// credentials — print them or they are lost.
					container.Logger.Warn("Provisioned but secret sync FAILED — record these credentials now, they are not stored anywhere",
						"app", appName)
					printCredentials(result.Credentials)
				}
				return err
			}

			if result.Synced {
				container.Logger.Info("Successfully provisioned",
					"app", appName,
					"db_name", result.Options.DatabaseName,
					"db_created", result.Report.DatabaseCreated,
					"user_created", result.Report.UserCreated,
					"secrets", fmt.Sprintf("%d created, %d updated, %d deleted, %d unchanged",
						result.SecretsCreated, result.SecretsUpdated, result.SecretsDeleted, result.SecretsUnchanged))
				return nil
			}

			// Not synced: this terminal output is the only copy of the
			// generated credentials — print them or they are lost.
			container.Logger.Warn("Provisioned WITHOUT secret sync — record these credentials now, they are not stored anywhere",
				"app", appName)
			printCredentials(result.Credentials)
			return nil
		},
	}

	cmd.Flags().StringVar(&dbType, "type", "postgres", "Database type (postgres|mysql)")
	cmd.Flags().StringVar(&env, "env", "dev", "Target environment (dev|staging|prod)")
	cmd.Flags().StringVar(&dbUser, "user", "", "Override database user (default: auto-generated)")
	cmd.Flags().StringVar(&dbPass, "pass", "", "Override database password (default: secure random)")
	cmd.Flags().StringVar(&dbName, "db", "", "Override database name (default: auto-generated)")
	cmd.Flags().StringVar(&dbSchema, "schema", "", "Override schema name, postgres only (default: public)")
	cmd.Flags().StringVar(&dbHost, "host", "", "Override recorded database host (default: engine config)")
	cmd.Flags().IntVar(&dbPort, "port", 0, "Override recorded database port (default: engine config)")
	cmd.Flags().BoolVar(&adopt, "adopt", false, "Adopt a pre-existing database/user and reconcile secrets (resets the app's password)")
	cmd.Flags().BoolVar(&force, "force", false, "Skip the adoption confirmation prompt (only meaningful with --adopt)")

	return cmd
}

// printCredentials dumps the secret contract to stdout as KEY=VALUE lines.
// Used whenever the terminal is the only place the generated credentials
// exist (offline provisioning, failed sync). stdout is data by convention, so
// the dump survives piping and redirection.
func printCredentials(creds database.Credentials) {
	for _, kv := range creds.ToSecrets() {
		fmt.Printf("%s=%s\n", kv.Key, kv.Value)
	}
}
