package cmd

import (
	"github.com/praaatik/databasemanager/internal/app"
	"github.com/spf13/cobra"
)

// NewRootCmd creates the root command with dependencies injected.
func NewRootCmd(container *app.Container) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "databasemanager",
		Short: "Provision databases and sync secrets",
		Long: `databasemanager is a CLI tool for provisioning isolated PostgreSQL and MySQL databases
and securely managing their credentials in Infisical Secrets Manager.`,
		SilenceUsage: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			verbose, _ := cmd.Flags().GetBool("verbose")
			if verbose {
				// TODO: I need to finish shaping the root Cobra command
				// ...inject the app container, wire up config + verbose flags,
				// ...decide how I want to handle logger level changes during PersistentPreRun,
				// ...and re-enable each subcommand once I complete their refactors
				// ...(provision, delete, list, conn, test).
			}
		},
	}

	cmd.PersistentFlags().String("config", "", "config file (default is $HOME/.databasemanager.yaml)")
	cmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose logging")

	cmd.AddCommand(newProvisionCmd(container))

	return cmd
}
