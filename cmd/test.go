package cmd

import (
	"fmt"

	infisical "github.com/infisical/go-sdk"
	"github.com/spf13/cobra"
)

var (
	testEnv string
	testApp string
)

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "Test database and Infisical connections",
	Long: `Test connectivity to the database and Infisical.
Optionally test a specific app's database credentials.

Examples:
  databasemanager test                    # Test admin connections
  databasemanager test --app myapp        # Test specific app's DB credentials
  databasemanager test --env prod`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger, err := GetLogger(cmd)
		if err != nil {
			logger.Errorf("unable to get logger: %v", err)
			fmt.Fprintf(cmd.ErrOrStderr(), "Unable to get logger: %v", err)
		}

		infisicalClient := GetInfisicalClient(cmd)
		cfg := GetConfig(cmd)
		databaseClient := GetDatabaseClient(cmd)

		logger.Info("Testing Infisical connection...\n")
		fmt.Fprintf(cmd.OutOrStdout(), "Testing Infisical connection...\n")

		if err := testInfisical(infisicalClient); err != nil {
			logger.Errorf("failed to test Infisical connection: %v\n", err)
			return err
		}

		logger.Info("testing database connection\n")
		fmt.Fprintln(cmd.OutOrStdout(), "Testing database connection...\n")

		if err := databaseClient.Test(cmd.Context()); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Failed: %v\n", err)
			logger.Errorf("database connection failed: %v", err)
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "OK\n")
		logger.Info("database connection working\n")

		if testApp != "" {
			fmt.Printf("Testing app %q credentials... ", testApp)
			fmt.Fprintf(cmd.OutOrStdout(), "Testing app %q credentials... ", testApp)
			logger.Infof("testing app %q credentials", testApp)

			secretPath := fmt.Sprintf("/%s", testApp)
			secrets, err := infisicalClient.Secrets().List(infisical.ListSecretsOptions{
				Environment: testEnv,
				ProjectID:   cfg.InfisicalProjectId,
				SecretPath:  secretPath,
			})

			if err != nil {
				fmt.Fprintf(cmd.OutOrStderr(), "Failed to fetch secrets: %v\n", err)
				logger.Errorf("failed to fetch secrets: %v\n", err)

				return err
			}

			if len(secrets) == 0 {
				fmt.Printf("No secrets found for app %q\n", testApp)
				logger.Infof("secrets found for app %q connection working\n", testApp)

				return fmt.Errorf("app %q not found", testApp)
			}

			secretMap := make(map[string]string)
			for _, s := range secrets {
				secretMap[s.SecretKey] = s.SecretValue
			}

			if err := databaseClient.TestAppConnection(cmd.Context(), secretMap); err != nil {
				fmt.Printf("Failed: %v\n", err)
				logger.Errorf("failed to connect to application database %v\n", err)

				return err
			}
			fmt.Println("OK\n")
			logger.Infof("application database connection working")
		}

		fmt.Println("\nAll tests passed!")
		logger.Infof("all tests passed")

		return nil
	},
}

func testInfisical(infisicalClient infisical.InfisicalClientInterface) error {
	if infisicalClient == nil {
		return fmt.Errorf("infisical client not initialized")
	}
	return nil
}

func init() {
	rootCmd.AddCommand(testCmd)
	testCmd.Flags().StringVar(&testEnv, "env", "dev", "Infisical environment")
	testCmd.Flags().StringVar(&testApp, "app", "", "App name to test credentials for (optional)")
}
