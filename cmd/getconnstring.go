package cmd

import (
	"fmt"

	infisical "github.com/infisical/go-sdk"
	"github.com/spf13/cobra"
)

var (
	getConnEnv    string
	getConnFormat string
)

var getconnstringCmd = &cobra.Command{
	Use:   "getconnstring <app>",
	Short: "Get database connection string for an app",
	Long: `Generate connection strings for an app's database in various formats.
Supports URI, psql command, and env file formats (for now).

Examples:
  databasemanager getconnstring myapp
  databasemanager getconnstring myapp --env prod
  databasemanager getconnstring myapp --format uri
  databasemanager getconnstring myapp --format psql
  databasemanager getconnstring myapp --format env`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]
		infClient := GetInfisicalClient(cmd)
		cfg := GetConfig(cmd)

		secretPath := fmt.Sprintf("/%s", appName)
		secrets, err := infClient.Secrets().List(infisical.ListSecretsOptions{
			Environment: getConnEnv,
			ProjectID:   cfg.InfisicalProjectId,
			SecretPath:  secretPath,
		})

		if err != nil {
			return fmt.Errorf("failed to fetch secrets for app %q: %w", appName, err)
		}

		if len(secrets) == 0 {
			return fmt.Errorf("no secrets found for app %q in environment %q", appName, getConnEnv)
		}

		secretMap := make(map[string]string)
		for _, s := range secrets {
			secretMap[s.SecretKey] = s.SecretValue
		}

		requiredKeys := []string{"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD", "DB_SCHEMA"}
		for _, key := range requiredKeys {
			if _, exists := secretMap[key]; !exists {
				// fairly certain this would never reach considering the provision stage, but still
				return fmt.Errorf("required secret %q not found", key)
			}
		}

		connString, err := generateConnectionString(secretMap, getConnFormat)
		if err != nil {
			return err
		}

		fmt.Println(connString)
		return nil
	},
}

// generateConnectionString generates the connection string based on the secrets stored in Infisical.
// Returns the connection string based on the flags user selects.
// Default format - URI
func generateConnectionString(secrets map[string]string, format string) (string, error) {
	host := secrets["DB_HOST"]
	port := secrets["DB_PORT"]
	dbName := secrets["DB_NAME"]
	user := secrets["DB_USER"]
	password := secrets["DB_PASSWORD"]
	schema := secrets["DB_SCHEMA"]

	switch format {
	case "uri":
		return fmt.Sprintf("postgresql://%s:%s@%s:%s/%s?search_path=%s", user, password, host, port, dbName, schema), nil

	case "psql":
		return fmt.Sprintf("psql -h %s -p %s -d %s -U %s", host, port, dbName, user), nil

	case "env":
		return fmt.Sprintf("DATABASE_URL=postgresql://%s:%s@%s:%s/%s?search_path=%s", user, password, host, port, dbName, schema), nil

	default:
		return "", fmt.Errorf("invalid format %q, must be: uri, psql, env", format)
	}
}

func init() {
	rootCmd.AddCommand(getconnstringCmd)
	getconnstringCmd.Flags().StringVar(&getConnEnv, "env", "dev", "Infisical environment")
	getconnstringCmd.Flags().StringVar(&getConnFormat, "format", "uri", "Output format: uri, psql, env")
}
