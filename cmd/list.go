package cmd

import (
	"fmt"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"

	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/spf13/cobra"
)

var (
	listApp         string
	listEnvironment string
	listType        string
	listEnv         string
	listShowValue   bool
)

const (
	typeApps    = "apps"
	typeSecrets = "secrets"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "list will list all the secrets stored in Infisical",
	Long: `List all secrets from Infisical and optionally list all databases 
from the connected database server.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if listType == "" {
			return fmt.Errorf("--type is required (options: apps, secrets)")
		}

		if !isValidType(listType) {
			return fmt.Errorf("invalid type %q, must be one of: %s", listType, strings.Join([]string{typeApps, typeSecrets}, ", "))
		}

		if listType == typeSecrets && listApp == "" {
			return fmt.Errorf("--app is required when --type is secrets")
		}

		infisicalClient := GetInfisicalClient(cmd)
		cfg := GetConfig(cmd)

		switch listType {
		case typeApps:
			return listApps(cmd, infisicalClient, cfg)
		case typeSecrets:
			return listSecrets(cmd, infisicalClient, cfg)
		default:
			return fmt.Errorf("unknown type: %s", listType)
		}
	},
}

// listApps retrieves and displays all apps (folders) in the Infisical project
func listApps(cmd *cobra.Command, infClient infisical.InfisicalClientInterface, cfg *config.Config) error {
	fmt.Printf("Fetching apps from Infisical (environment: %s)...\n\n", listEnv)

	folders, err := infClient.Folders().List(infisical.ListFoldersOptions{
		ProjectID:   cfg.InfisicalProjectId,
		Environment: listEnv,
	})

	if err != nil {
		return fmt.Errorf("failed to list apps: %w", err)
	}

	if len(folders) == 0 {
		fmt.Printf("No apps found in environment %q\n", listEnv)
		return nil
	}

	t := table.NewWriter()
	t.AppendHeader(table.Row{"App Name"})

	for _, folder := range folders {
		t.AppendRow(table.Row{folder.Name})
	}

	fmt.Println(t.Render())
	return nil
}

// listSecrets retrieves and displays all secrets for a specific app
func listSecrets(cmd *cobra.Command, infClient infisical.InfisicalClientInterface, cfg *config.Config) error {
	secretPath := fmt.Sprintf("/%s", listApp)

	fmt.Printf("Fetching secrets for app %q (environment: %s)...\n\n", listApp, listEnv)

	secrets, err := infClient.Secrets().List(infisical.ListSecretsOptions{
		Environment: listEnv,
		ProjectID:   cfg.InfisicalProjectId,
		SecretPath:  secretPath,
	})

	if err != nil {
		return fmt.Errorf("failed to list secrets for app %q: %w", listApp, err)
	}

	if len(secrets) == 0 {
		fmt.Printf("No secrets found for app %q in environment %q\n", listApp, listEnv)
		return nil
	}

	t := table.NewWriter()
	t.AppendHeader(table.Row{"Secret Key", "Secret Value"})

	for _, secret := range secrets {
		value := secret.SecretValue
		if !listShowValue {
			value = maskSecretValue(secret.SecretKey, secret.SecretValue)
		}
		t.AppendRow(table.Row{secret.SecretKey, value})
	}

	fmt.Println(t.Render())
	return nil
}

// maskSecretValue masks sensitive values based on the secret key
func maskSecretValue(key, value string) string {
	// I am adding this, so I could filter out SECRETSs and TOKENs later on.
	// Keeping just PASSWORD for DB_PASSWORD for now.
	sensitiveKeywords := []string{"PASSWORD"}

	keyUpper := strings.ToUpper(key)
	for _, keyword := range sensitiveKeywords {
		// TODO: match length, maybe display first/last characters?
		if strings.Contains(keyUpper, keyword) {
			return "*****"
		}
	}

	return value
}

// isValidType checks if the provided type is valid
func isValidType(t string) bool {
	validTypes := []string{typeApps, typeSecrets}
	for _, valid := range validTypes {
		if t == valid {
			return true
		}
	}
	return false
}

func init() {
	rootCmd.AddCommand(listCmd)

	listCmd.Flags().StringVar(&listType, "type", "", "Type to list: apps, secrets (required)")
	listCmd.Flags().StringVar(&listApp, "app", "", "Application name (required when --type is secrets)")
	listCmd.Flags().StringVar(&listEnv, "env", "dev", "Infisical environment")
	listCmd.Flags().BoolVar(&listShowValue, "show-values", false, "Show actual secret values (default: masked for sensitive data)")
}
