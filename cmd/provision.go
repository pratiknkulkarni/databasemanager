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
		dbType   string
		dbUser   string
		dbPass   string
		dbName   string
		dbSchema string
		dbHost   string
		dbPort   int
		env      string
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

			provisioner := services.NewProvisioner(services.ProvisionerDeps{
				DB:        db,
				Secrets:   container.Infisical,
				Logger:    container.Logger,
				ProjectID: container.Config.InfisicalProjectID,
				Engine:    dbType,
				EngineCfg: engineCfg,
			})

			req := services.ProvisionRequest{
				AppName:     appName,
				Environment: env,
				DBName:      dbName,
				DBUser:      dbUser,
				DBPassword:  dbPass,
				DBSchema:    dbSchema,
				Host:        dbHost,
				Port:        dbPort,
			}

			result, err := provisioner.Run(ctx, req)
			if err != nil {
				return err
			}

			if result.Synced {
				container.Logger.Info("Successfully provisioned",
					"app", appName,
					"db_name", result.Options.DatabaseName,
					"connection", "Secrets synced to Infisical")
				return nil
			}

			// Not synced: this terminal output is the only copy of the
			// generated credentials — print them or they are lost.
			container.Logger.Warn("Provisioned WITHOUT secret sync — record these credentials now, they are not stored anywhere",
				"app", appName)
			for _, kv := range result.Credentials.ToSecrets() {
				fmt.Printf("%s=%s\n", kv.Key, kv.Value)
			}
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

	return cmd
}
