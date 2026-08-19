package cmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/log"
	infisical "github.com/infisical/go-sdk"
	"github.com/infisical/go-sdk/packages/models"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/pratiknkulkarni/databasemanager/internal/app"
	"github.com/pratiknkulkarni/databasemanager/internal/config"
	"github.com/spf13/cobra"
)

func newListCmd(container *app.Container) *cobra.Command {
	var (
		listFormat    string
		listEnv       string
		listShowValue bool
	)

	cmd := &cobra.Command{
		Use:   "list [APP] [SECRET_KEY]",
		Short: "List secrets stored in Infisical",
		Long: `List all apps, all secrets in an app, or a specific secret.

Examples:
  databasemanager list              					# List all apps
  databasemanager list myapp        					# List all secrets in myapp
  databasemanager list myapp DB_PASSWORD  				# Get specific secret
  databasemanager list myapp --show-values				# Display sensitive information instead of masking it
  databasemanager list myapp DB_PASSWORD --format json	# Display output in json format instead of table`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if container.Infisical == nil {
				return fmt.Errorf("infisical client not initialized")
			}

			switch len(args) {
			case 0:
				return listApps(container.Infisical, container.Config, container.Logger, listEnv, listFormat)
			case 1:
				return listSecrets(container.Infisical, container.Config, container.Logger, args[0], listEnv, listFormat, listShowValue)
			case 2:
				return getSecret(container.Infisical, container.Config, container.Logger, args[0], args[1], listEnv, listFormat, listShowValue)
			default:
				return fmt.Errorf("too many arguments: expected 0-2, got %d", len(args))
			}
		},
	}

	cmd.Flags().StringVar(&listEnv, "env", "dev", "Infisical environment")
	cmd.Flags().BoolVar(&listShowValue, "show-values", false, "Show actual secret values (default: masked for sensitive data)")
	cmd.Flags().StringVar(&listFormat, "format", "table", "Output format: table, json")

	return cmd
}

// listApps retrieves and displays all apps (folders) in the Infisical project.
// Status messages go to the logger (stderr); only data is written to stdout.
func listApps(client infisical.InfisicalClientInterface, cfg *config.Config, logger *log.Logger, env, format string) error {
	logger.Info("Fetching apps from Infisical", "env", env)

	folders, err := client.Folders().List(infisical.ListFoldersOptions{
		ProjectID:   cfg.InfisicalProjectID,
		Environment: env,
	})

	if err != nil {
		return fmt.Errorf("failed to list apps: %w", err)
	}

	if len(folders) == 0 {
		if format == "json" {
			fmt.Println("[]")
		} else {
			fmt.Printf("No apps found in environment %q\n", env)
		}
		return nil
	}

	if format == "json" {
		return outputAppsJSON(folders)
	}
	return outputAppsTable(folders)
}

// listSecrets retrieves and displays all secrets for a specific app
func listSecrets(client infisical.InfisicalClientInterface, cfg *config.Config, logger *log.Logger, appName, env, format string, showValues bool) error {
	secretPath := fmt.Sprintf("/%s", appName)

	logger.Info("Fetching secrets", "app", appName, "env", env)

	secrets, err := client.Secrets().List(infisical.ListSecretsOptions{
		Environment: env,
		ProjectID:   cfg.InfisicalProjectID,
		SecretPath:  secretPath,
	})

	if err != nil {
		return fmt.Errorf("failed to list secrets for app %q: %w", appName, err)
	}

	if len(secrets) == 0 {
		if format == "json" {
			fmt.Println("[]")
		} else {
			fmt.Printf("No secrets found for app %q in environment %q\n", appName, env)
		}
		return nil
	}

	if format == "json" {
		return outputSecretsJSON(secrets, showValues)
	}
	return outputSecretsTable(secrets, showValues)
}

// getSecret retrieves and displays a secret for a specific app for a key
func getSecret(client infisical.InfisicalClientInterface, cfg *config.Config, logger *log.Logger, appName, secretKey, env, format string, showValues bool) error {
	secretPath := fmt.Sprintf("/%s", appName)

	logger.Info("Fetching secret", "key", secretKey, "app", appName, "env", env)

	secrets, err := client.Secrets().List(infisical.ListSecretsOptions{
		Environment: env,
		ProjectID:   cfg.InfisicalProjectID,
		SecretPath:  secretPath,
	})

	if err != nil {
		return fmt.Errorf("failed to list secrets for app %q: %w", appName, err)
	}

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

	if format == "json" {
		return outputSecretJSON(foundSecret, showValues)
	}
	return outputSecretTable(foundSecret, showValues)
}

func outputAppsJSON(folders []models.Folder) error {
	data := make([]map[string]string, 0, len(folders))
	for _, folder := range folders {
		data = append(data, map[string]string{"app_name": folder.Name})
	}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(jsonData))
	return nil
}

func outputAppsTable(folders []models.Folder) error {
	t := table.NewWriter()
	t.AppendHeader(table.Row{"App Name"})
	for _, folder := range folders {
		t.AppendRow(table.Row{folder.Name})
	}
	fmt.Println(t.Render())
	return nil
}

func outputSecretsTable(secrets []models.Secret, showValues bool) error {
	t := table.NewWriter()
	t.AppendHeader(table.Row{"Secret Key", "Secret Value"})
	for _, secret := range secrets {
		value := secret.SecretValue
		if !showValues {
			value = maskSecretValue(secret.SecretKey, secret.SecretValue)
		}
		t.AppendRow(table.Row{secret.SecretKey, value})
	}
	fmt.Println(t.Render())
	return nil
}

func outputSecretsJSON(secrets []models.Secret, showValues bool) error {
	data := make([]map[string]string, 0, len(secrets))
	for _, secret := range secrets {
		value := secret.SecretValue
		if !showValues {
			value = maskSecretValue(secret.SecretKey, secret.SecretValue)
		}
		data = append(data, map[string]string{
			"secret_key":   secret.SecretKey,
			"secret_value": value,
		})
	}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(jsonData))
	return nil
}

func outputSecretTable(secret *models.Secret, showValues bool) error {
	t := table.NewWriter()
	t.AppendHeader(table.Row{"Secret Key", "Secret Value"})
	value := secret.SecretValue
	if !showValues {
		value = maskSecretValue(secret.SecretKey, secret.SecretValue)
	}
	t.AppendRow(table.Row{secret.SecretKey, value})
	fmt.Println(t.Render())
	return nil
}

func outputSecretJSON(secret *models.Secret, showValues bool) error {
	value := secret.SecretValue
	if !showValues {
		value = maskSecretValue(secret.SecretKey, secret.SecretValue)
	}
	data := map[string]string{
		"secret_key":   secret.SecretKey,
		"secret_value": value,
	}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(jsonData))
	return nil
}

// credentialURIPattern matches the `user:password@` userinfo of a connection
// URI so an embedded password can be masked even when the secret's key looks
// innocuous (e.g. DB_URI, DSN).
var credentialURIPattern = regexp.MustCompile(`(://[^:/@\s]+:)([^@/\s]+)(@)`)

func maskSecretValue(key, value string) string {
	sensitiveKeywords := []string{"PASSWORD", "PASSWD", "PWD", "KEY", "SECRET", "TOKEN", "CREDENTIAL"}
	keyUpper := strings.ToUpper(key)
	for _, keyword := range sensitiveKeywords {
		if strings.Contains(keyUpper, keyword) {
			return "*****"
		}
	}
	// Even under a non-sensitive key, a value carrying `scheme://user:pass@host`
	// leaks a password in cleartext — mask just the password portion.
	return credentialURIPattern.ReplaceAllString(value, "${1}*****${3}")
}
