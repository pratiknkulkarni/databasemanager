package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/infisical/go-sdk/packages/models"
	"github.com/jedib0t/go-pretty/v6/table"

	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/spf13/cobra"
)

var (
	listFormat    string
	listEnv       string
	listShowValue bool
)

var listCmd = &cobra.Command{
	Use:   "list [APP] [SECRET_KEY]",
	Short: "list will list all the secrets stored in Infisical",
	Long: `List all apps, all secrets in an app, or a specific secret.
   
	Examples:
	  databasemanager list              					# List all apps
	  databasemanager list myapp        					# List all secrets in myapp
	  databasemanager list myapp DB_PASSWORD  				# Get specific secret
	  databasemanager list myapp --show-values				# Display sensitive information instead of masking it
	  databasemanager list myapp DB_PASSWORD --format json	# Display output in json format instead of table`,
	RunE: func(cmd *cobra.Command, args []string) error {
		infisicalClient := GetInfisicalClient(cmd)
		cfg := GetConfig(cmd)

		switch len(args) {
		case 0:
			return listApps(infisicalClient, cfg)
		case 1:
			//listApp = args[0]
			return listSecrets(infisicalClient, cfg, args[0])
		case 2:
			return getSecret(infisicalClient, cfg, args[0], args[1])
		default:
			return fmt.Errorf("too many arguments: expected 0-2, got %d", len(args))
		}
	},
}

// listApps retrieves and displays all apps (folders) in the Infisical project
func listApps(infisicalClient infisical.InfisicalClientInterface, cfg *config.Config) error {
	fmt.Printf("Fetching apps from Infisical (environment: %s)...\n\n", listEnv)

	folders, err := infisicalClient.Folders().List(infisical.ListFoldersOptions{
		ProjectID:   cfg.InfisicalProjectId,
		Environment: listEnv,
	})

	if err != nil {
		return fmt.Errorf("failed to list apps: %w", err)
	}

	if len(folders) == 0 {
		if listFormat == "json" {
			fmt.Println("[]")
		} else {
			fmt.Printf("No apps found in environment %q\n", listEnv)
		}
		return nil
	}

	if listFormat == "json" {
		return outputAppsJSON(folders)
	}

	return outputAppsTable(folders)
}

// outputAppsJSON renders apps in JSON format
func outputAppsJSON(folders []models.Folder) error {
	data := make([]map[string]string, 0, len(folders))
	for _, folder := range folders {
		data = append(data, map[string]string{
			"app_name": folder.Name,
		})
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal apps to JSON: %w", err)
	}

	fmt.Println(string(jsonData))
	return nil
}

// outputAppsTable renders apps in table format
func outputAppsTable(folders []models.Folder) error {
	t := table.NewWriter()
	t.AppendHeader(table.Row{"App Name"})

	for _, folder := range folders {
		t.AppendRow(table.Row{folder.Name})
	}

	fmt.Println(t.Render())
	return nil
}

// listSecrets retrieves and displays all secrets for a specific app
func listSecrets(infisicalClient infisical.InfisicalClientInterface, cfg *config.Config, appName string) error {
	secretPath := fmt.Sprintf("/%s", appName)

	fmt.Printf("Fetching secrets for app %q (environment: %s)...\n\n", appName, listEnv)

	secrets, err := infisicalClient.Secrets().List(infisical.ListSecretsOptions{
		Environment: listEnv,
		ProjectID:   cfg.InfisicalProjectId,
		SecretPath:  secretPath,
	})

	if err != nil {
		return fmt.Errorf("failed to list secrets for app %q: %w", appName, err)
	}

	if len(secrets) == 0 {
		if listFormat == "json" {
			fmt.Println("[]")
		} else {
			fmt.Printf("No secrets found for app %q in environment %q\n", appName, listEnv)
		}
		return nil
	}

	if listFormat == "json" {
		return outputSecretsJSON(secrets)
	}

	return outputSecretsTable(secrets)
}

// getSecret retrieves and displays a secret for a specific app for a key
func getSecret(infisicalClient infisical.InfisicalClientInterface, cfg *config.Config, appName, secretKey string) error {
	secretPath := fmt.Sprintf("/%s", appName)

	fmt.Printf("Fetching secret %q from app %q (environment: %s)...\n\n", secretKey, appName, listEnv)

	secrets, err := infisicalClient.Secrets().List(infisical.ListSecretsOptions{
		Environment: listEnv,
		ProjectID:   cfg.InfisicalProjectId,
		SecretPath:  secretPath,
	})

	if err != nil {
		return fmt.Errorf("failed to list secrets for app %q: %w", appName, err)
	}

	// Find the specific secret
	var foundSecret *models.Secret
	for i := range secrets {
		if secrets[i].SecretKey == secretKey {
			foundSecret = &secrets[i]
			break
		}
	}

	if foundSecret == nil {
		return fmt.Errorf("failed to find secret %q in app %q", secretKey, appName)
	}

	if listFormat == "json" {
		return outputSecretJSON(foundSecret)
	}

	return outputSecretTable(foundSecret)
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

// outputSecretsTable renders secrets in table format
func outputSecretsTable(secrets []models.Secret) error {
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

// outputSecretsJSON displays secrets in JSON format
func outputSecretsJSON(secrets []models.Secret) error {
	data := make([]map[string]string, 0, len(secrets))
	for _, secret := range secrets {
		value := secret.SecretValue
		if !listShowValue {
			value = maskSecretValue(secret.SecretKey, secret.SecretValue)
		}
		data = append(data, map[string]string{
			"secret_key":   secret.SecretKey,
			"secret_value": value,
		})
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal secrets to JSON: %w", err)
	}

	fmt.Println(string(jsonData))
	return nil
}

// outputSecretTable renders a single secret in table format
func outputSecretTable(secret *models.Secret) error {
	t := table.NewWriter()
	t.AppendHeader(table.Row{"Secret Key", "Secret Value"})

	value := secret.SecretValue
	if !listShowValue {
		value = maskSecretValue(secret.SecretKey, secret.SecretValue)
	}
	t.AppendRow(table.Row{secret.SecretKey, value})

	fmt.Println(t.Render())
	return nil
}

// outputSecretJSON renders a single secret in JSON format
func outputSecretJSON(secret *models.Secret) error {
	value := secret.SecretValue
	if !listShowValue {
		value = maskSecretValue(secret.SecretKey, secret.SecretValue)
	}

	data := map[string]string{
		"secret_key":   secret.SecretKey,
		"secret_value": value,
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal secret to JSON: %w", err)
	}

	fmt.Println(string(jsonData))
	return nil
}

func init() {
	rootCmd.AddCommand(listCmd)

	listCmd.Flags().StringVar(&listEnv, "env", "dev", "Infisical environment")
	listCmd.Flags().BoolVar(&listShowValue, "show-values", false, "Show actual secret values (default: masked for sensitive data)")
	listCmd.Flags().StringVar(&listFormat, "format", "table", "Output format: table, json")
}
