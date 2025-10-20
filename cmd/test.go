/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
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
		infisicalClient := GetInfisicalClient(cmd)
		cfg := GetConfig(cmd)

		//TODO: handle this error instead of ignoring it
		databaseClient, _ := GetDatabaseClient(cmd)

		fmt.Print("Testing Infisical connection... ")
		if err := testInfisical(infisicalClient); err != nil {
			fmt.Printf("Failed: %v\n", err)
			return err
		}
		fmt.Println("OK")

		fmt.Print("Testing database connection... ")
		if err := databaseClient.Test(cmd.Context()); err != nil {
			fmt.Printf("Failed: %v\n", err)
			return err
		}
		fmt.Println("OK")

		if testApp != "" {
			fmt.Printf("Testing app %q credentials... ", testApp)

			secretPath := fmt.Sprintf("/%s", testApp)
			secrets, err := infisicalClient.Secrets().List(infisical.ListSecretsOptions{
				Environment: testEnv,
				ProjectID:   cfg.InfisicalProjectId,
				SecretPath:  secretPath,
			})

			if err != nil {
				fmt.Printf("Failed to fetch secrets: %v\n", err)
				return err
			}

			if len(secrets) == 0 {
				fmt.Printf("No secrets found for app %q\n", testApp)
				return fmt.Errorf("app %q not found", testApp)
			}

			secretMap := make(map[string]string)
			for _, s := range secrets {
				secretMap[s.SecretKey] = s.SecretValue
			}

			fmt.Println("secretMap -> ", secretMap)
			if err := databaseClient.TestAppConnection(cmd.Context(), secretMap); err != nil {
				fmt.Printf("Failed: %v\n", err)
				return err
			}
			fmt.Println("OK")
		}

		fmt.Println("\nAll tests passed!")
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
