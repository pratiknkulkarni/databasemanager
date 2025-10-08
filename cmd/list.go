package cmd

import (
	"context"
	"fmt"

	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "list will list all the secrets stored in Infisical",
	Long: `List all secrets from Infisical and optionally list all databases 
from the connected database server.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Get clients from context
		infisicalClient := GetInfisicalClient(cmd)
		//dbClient := GetDatabaseClient(cmd)
		cfg := GetConfig(cmd)

		environment, _ := cmd.Flags().GetString("environment")
		secretPath, _ := cmd.Flags().GetString("secret-path")

		return list(cmd.Context(), infisicalClient, cfg, environment, secretPath)
	},
}

func init() {
	fmt.Println("calling list init")
	rootCmd.AddCommand(listCmd)

	// Add list-specific flags if needed
	listCmd.Flags().String("environment", "dev", "Infisical environment to list secrets from")
	listCmd.Flags().String("secret-path", "/", "Path to secrets in Infisical")
	listCmd.Flags().Bool("list-databases", false, "Also list databases from the database server")
}

// list will list the existing secrets in different formats
// TODO: update this ridiculous docstring ffs
func list(ctx context.Context, infisicalClient *infisical.InfisicalClient, cfg *config.Config, environment, secretPath string) error {
	fmt.Println("=== Listing Secrets from Infisical ===")

	fmt.Println(cfg.InfisicalProjectId, environment, secretPath, infisicalClient)
	_, _ = infisicalClient.Secrets().List(infisical.ListSecretsOptions{
		Environment: environment,
		ProjectID:   cfg.InfisicalProjectId,
		SecretPath:  secretPath,
	})
	//
	//fmt.Println(secrets, err)
	//
	//if err != nil {
	//	return fmt.Errorf("failed to list secrets: %w", err)
	//}
	//
	//fmt.Printf("\nFound %d secrets in environment '%s':\n", len(secrets), environment)
	//for _, secret := range secrets {
	//	fmt.Printf("  - %s\n", secret.SecretKey)
	//}

	return nil
}
