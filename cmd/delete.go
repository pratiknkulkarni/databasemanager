package cmd

import (
	"bufio"
	"fmt"
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
		// TODO: handle this error instead of ignoring it
		//databaseClient, _ := GetDatabaseClient(cmd)
		databaseClient, err := initDatabaseClient(cfg, cmd)

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
			getDeleteConfirmation(cmd, appName, dbName, dbUser, deleteEnv)
		}

		err = databaseClient.Delete(cmd.Context(), dbName, dbUser)

		if err != nil {
			return fmt.Errorf("failed to delete database: %w", err)
		}

		for _, secret := range secrets {
			deleteOptions := infisical.DeleteSecretOptions{
				Environment: deleteEnv,
				ProjectID:   cfg.InfisicalProjectId,
				SecretPath:  secretPath,
				SecretKey:   secret.SecretKey,
			}

			_, err := infisicalClient.Secrets().Delete(deleteOptions)
			if err != nil {
				return fmt.Errorf("failed to delete secret from app: %w", err)
			}
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
func getDeleteConfirmation(cmd *cobra.Command, appName, dbName, dbUser, env string) bool {
	out := cmd.ErrOrStderr()
	in := cmd.InOrStdin()

	fmt.Fprintln(out, "You are about to delete app:", appName)
	fmt.Fprintf(out, "Environment: %s\n\n", env)
	fmt.Fprintln(out, "This will delete:")
	fmt.Fprintf(out, " - App from Infisical: /%s\n\n", appName)
	fmt.Fprintf(out, "  - DatabaseName: %s\n", dbName)
	fmt.Fprintf(out, "  - DatabaseName user: %s\n", dbUser)
	fmt.Fprint(out, "Type 'y' to confirm deletion: ")

	reader := bufio.NewReader(in)
	input, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "\nfailed to read confirmation: ", err)
	}

	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y"
}

func init() {
	rootCmd.AddCommand(deleteCmd)

	deleteCmd.Flags().StringVarP(&deleteEnv, "env", "e", "dev", "Infisical environment to write secrets into")
	deleteCmd.Flags().BoolVarP(&deleteForce, "force", "f", false, "Force delete without user confirmation.")
}
