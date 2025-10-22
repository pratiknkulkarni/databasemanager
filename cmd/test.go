package cmd

import (
	"fmt"

	"github.com/charmbracelet/log"
	infisical "github.com/infisical/go-sdk"
	"github.com/spf13/cobra"
)

var (
	testEnv string
	testApp string
)

// TODO:  see if I can format this a bit better. The current is just meh
var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test database and Infisical connections",
	Args:  cobra.MaximumNArgs(1),
	Long: `Test connectivity to the database and Infisical.
Optionally test a specific app's database credentials.

Examples:
  databasemanager test                    # Test admin connections
  databasemanager test --app myapp        # Test specific app's DB credentials
  databasemanager test --env prod`,
	RunE: func(cmd *cobra.Command, args []string) error {
		//var appName string
		if len(args) == 1 {
			testApp = args[0]
		}

		logger, err := GetLogger(cmd)
		if err != nil {
			//fmt.Fprintf(cmd.ErrOrStderr(), "Unable to get logger: %v\n", err)
			return err
		}

		infisicalClient, err := GetInfisicalClient(cmd)
		if err != nil {
			//fmt.Fprintf(cmd.ErrOrStderr(), "infisical client could not be initialized: %v\n", err)
			logger.Debugf("infisical client could not be initialized: %v\n", err)
			return err
		}

		cfg := GetConfig(cmd)
		databaseClient := GetDatabaseClient(cmd)

		logger.Debug("Testing Infisical connection...\n")
		fmt.Fprintf(cmd.OutOrStdout(), "Testing Infisical connection...\n")

		if err := testInfisical(infisicalClient, logger); err != nil {
			//logger.Errorf("failed to test Infisical connection: %v\n", err)
			return err
		}

		logger.Debug("testing database connection\n")
		fmt.Fprintln(cmd.OutOrStdout(), "Testing database connection...\n")

		if err := databaseClient.Test(cmd.Context()); err != nil {
			//fmt.Fprintf(cmd.ErrOrStderr(), "Failed: %v\n", err)
			logger.Errorf("database connection failed: %v", err)
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "OK\n")
		logger.Info("database connection working\n")

		if testApp != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Testing app %q credentials...\n", testApp)
			logger.Debugf("testing app %q credentials", testApp)

			secretPath := fmt.Sprintf("/%s", testApp)
			secrets, _ := infisicalClient.Secrets().List(infisical.ListSecretsOptions{
				Environment: testEnv,
				ProjectID:   cfg.InfisicalProjectId,
				SecretPath:  secretPath,
			})

			if len(secrets) == 0 {
				logger.Debugf("credentials for app %q not found\n", testApp)
				return fmt.Errorf("credentials for app %q not found", testApp)
			}

			secretMap := make(map[string]string)
			for _, s := range secrets {
				secretMap[s.SecretKey] = s.SecretValue
			}

			if err := databaseClient.TestAppConnection(cmd.Context(), secretMap); err != nil {
				//fmt.Fprintf(cmd.ErrOrStderr(), "Failed: %v\n", err)
				//logger.Errorf("failed to connect to application database %v\n", err)

				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "OK")
			logger.Infof("application database connection working")
		}

		fmt.Fprintln(cmd.OutOrStdout(), "All tests passed!")
		logger.Infof("all tests passed")

		return nil
	},
}

// testInfisical checks if Infisical is reachable.
// It just checks if the infisicalClient is not nil.
func testInfisical(infisicalClient infisical.InfisicalClientInterface, logger *log.Logger) error {
	if infisicalClient == nil {
		logger.Errorf("infisical client not initialized")
		return fmt.Errorf("infisical client not initialized")
	}

	logger.Debug("infisical client initialized successfully")
	return nil
}

func init() {
	rootCmd.AddCommand(testCmd)
	testCmd.Flags().StringVar(&testEnv, "env", "dev", "Infisical environment")
	testCmd.Flags().StringVar(&testApp, "app", "", "App name to test credentials for (optional)")
}
