package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/infisical"
	"github.com/spf13/cobra"
)

// warnInsecureTLS emits a loud warning when an engine section explicitly
// disables transport encryption, so a cleartext dial is never silent.
func warnInsecureTLS(container *app.Container, engine string, cfg config.DatabaseConfig) {
	if strings.EqualFold(cfg.DatabaseSSLMode, "disable") || strings.EqualFold(cfg.DatabaseSSLMode, "false") {
		container.Logger.Warn("TLS is DISABLED for this connection — credentials travel in cleartext",
			"engine", engine)
	}
}

// NewRootCmd creates the root command. The container arrives with only the
// logger set; config and the Infisical client are populated here, after Cobra
// has parsed the persistent flags, so --config (both forms) and --verbose
// actually take effect.
func NewRootCmd(container *app.Container) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "databasemanager",
		Short: "Provision databases and sync secrets",
		Long: `databasemanager is a CLI tool for provisioning isolated PostgreSQL and MySQL databases
and securely managing their credentials in Infisical Secrets Manager.`,
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			verbose, _ := cmd.Flags().GetBool("verbose")
			if verbose {
				container.Logger.SetLevel(log.DebugLevel)
			}

			cfgPath, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}
			container.Config = cfg

			client, err := infisical.NewClient(cmd.Context(), cfg)
			if err != nil {
				// Degrading to offline is intentional, but the reason must not
				// be swallowed: a 401 here means the credentials are wrong for
				// the auth method in use, which is otherwise invisible until
				// secrets silently stop syncing.
				container.Logger.Warn("infisical authentication failed; continuing without secret sync",
					"auth_method", cfg.AuthMethod(),
					"site_url", cfg.InfisicalSiteURL,
					"err", err)
				container.Logger.Warn("if this is a 401, check that the identity's configured auth method " +
					"matches the credentials in your config file")
			} else {
				container.Infisical = client
			}

			return nil
		},
	}

	cmd.PersistentFlags().String("config", "", "config file (default is $HOME/.databasemanager.yaml)")
	cmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose logging")

	cmd.AddCommand(newProvisionCmd(container))
	cmd.AddCommand(newDeleteCmd(container))
	cmd.AddCommand(newListCmd(container))
	cmd.AddCommand(newConnCmd(container))
	cmd.AddCommand(newTestCmd(container))
	cmd.AddCommand(newRotateCmd(container))

	return cmd
}
