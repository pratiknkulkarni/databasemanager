package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	infisical "github.com/infisical/go-sdk"
	"github.com/spf13/cobra"
)

var (
	deleteEnv   string
	deleteForce bool
)

// deleteCmd represents the delete command
var deleteCmd = &cobra.Command{
	Use:   "delete <app>",
	Short: "Delete app database and Infisical secrets",
	Long: `Delete an app's database, database user, and remove its secrets from Infisical.

Without --force, you'll be prompted to confirm deletion of each resource.
With --force, all resources are deleted after a single confirmation.

The deletion process:
1. Terminates all active database connections
2. Drops the database
3. Drops the database user
4. Deletes the Infisical folder containing secrets

Examples:
  databasemanager delete myapp
  databasemanager delete myapp --env prod
  databasemanager delete myapp --force
  databasemanager delete myapp --env prod --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		appName := args[0]

		infisicalClient := GetInfisicalClient(cmd)
		cfg := GetConfig(cmd)
		databaseClient := GetDatabaseClient(cmd)

		secretPath := fmt.Sprintf("/%s", appName)
		secrets, err := infisicalClient.Secrets().List(infisical.ListSecretsOptions{
			Environment: deleteEnv,
			ProjectID:   cfg.InfisicalProjectId,
			SecretPath:  secretPath,
		})

		if err != nil {
			return fmt.Errorf("failed to fetch secrets for app %q: %w", appName, err)
		}

		// This should never reach considering how the app is being provisioned, unless I manually delete from Infisical
		if len(secrets) == 0 {
			return fmt.Errorf("app %q not found in environment %q (no secrets found)", appName, deleteEnv)
		}

		secretMap := make(map[string]string)
		for _, s := range secrets {
			secretMap[s.SecretKey] = s.SecretValue
		}

		dbName := secretMap["DB_NAME"]
		dbUser := secretMap["DB_USER"]

		if dbName == "" || dbUser == "" {
			return fmt.Errorf("incomplete secrets for app %q: missing DB_NAME or DB_USER. Unable to delete database without it", appName)
		}

		if !deleteForce {
			getDeleteConfirmation(appName, dbName, dbUser, deleteEnv)
		}

		err = databaseClient.Delete(cmd.Context(), dbName, dbUser)

		if err != nil {
			return fmt.Errorf("failed to delete database: %w", err)
		}

		_, err = infisicalClient.Folders().Delete(infisical.DeleteFolderOptions{
			FolderName:  appName,
			ProjectID:   cfg.InfisicalProjectId,
			Environment: deleteEnv,
			Path:        "/",
		})

		if err != nil {
			return fmt.Errorf("failed to delete app from Infisical: %w", err)
		} else {
			return nil
		}
	},
}

// getDeleteConfirmation prompts user to confirm deletion
func getDeleteConfirmation(appName, dbName, dbUser, env string) bool {
	fmt.Printf("You are about to delete app: %s\n", appName)
	fmt.Printf("Environment: %s\n\n", env)
	fmt.Printf("This will delete:\n")
	fmt.Printf(" - App from Infisical: /%s\n\n", appName)
	fmt.Printf("  - Database: %s\n", dbName)
	fmt.Printf("  - Database user: %s\n", dbUser)
	fmt.Print("Type 'y' to confirm deletion: ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	return input == "y"
}

func init() {
	rootCmd.AddCommand(deleteCmd)

	deleteCmd.Flags().StringVarP(&deleteEnv, "env", "e", "dev", "Infisical environment to write secrets into")
	deleteCmd.Flags().BoolVarP(&deleteForce, "force", "f", false, "Force delete without user confirmation.")
}
