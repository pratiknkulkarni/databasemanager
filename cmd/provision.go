package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"text/tabwriter"
	"time"

	infisical "github.com/infisical/go-sdk"
	"github.com/spf13/cobra"

	"github.com/praaatik/databasemanager/internal/database"
)

// TODO: update these variable names for PROVISION_ instead. This is causing confusion being in same package.
var (
	appPassword  string
	dbName       string
	userName     string
	schemaName   string
	hidePassword bool
	environment  string
)

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision a new isolated application database",
	Long:  `Provision a new database, roles, and schema for an application.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("arguments mismatch: expected 1 argument")
		}
		appName := args[0]
		client := GetDatabaseClient(cmd)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if appPassword == "" {
			appPassword = generateRandomPassword(16)
			fmt.Printf("Generated random password for app: %s", appName)
		}

		provisionOptions := database.ProvisionOptions{
			AppName:     args[0],
			AppPassword: appPassword,
			Database:    dbName,
			User:        userName,
			Schema:      schemaName,
		}

		if provisionOptions.Database == "" {
			provisionOptions.Database = fmt.Sprintf("%s_db", provisionOptions.AppName)
		}

		if provisionOptions.User == "" {
			provisionOptions.User = fmt.Sprintf("%s_user", provisionOptions.AppName)
		}

		if provisionOptions.Schema == "" {
			provisionOptions.Schema = fmt.Sprintf("%s_data", provisionOptions.AppName)
		}

		log.Printf("Provisioning database for app=%s ...", appName)

		if err := client.Provision(ctx, provisionOptions); err != nil {
			return fmt.Errorf("failed to provision database: %w", err)
		}

		infisicalClient := GetInfisicalClient(cmd)
		if infisicalClient == nil {
			log.Printf("no infisical client in the context")
		} else {
			cfg := GetConfig(cmd)
			env := "dev"
			if eFlag, _ := cmd.Flags().GetString("env"); eFlag != "" {
				env = eFlag
			}

			_, err := infisicalClient.Folders().Create(infisical.CreateFolderOptions{
				ProjectID:   cfg.InfisicalProjectId,
				Name:        provisionOptions.AppName,
				Environment: env,
			})

			if err != nil {
				return fmt.Errorf("failed to create folder in Infisical: %w", err)
			}

			secretPath := fmt.Sprintf("/%s", provisionOptions.AppName)
			secrets := []infisical.BatchCreateSecret{
				{SecretKey: "DB_NAME", SecretValue: provisionOptions.Database},
				{SecretKey: "DB_USER", SecretValue: provisionOptions.User},
				{SecretKey: "DB_PASSWORD", SecretValue: provisionOptions.AppPassword},
				{SecretKey: "DB_SCHEMA", SecretValue: provisionOptions.Schema},
				{SecretKey: "DB_HOST", SecretValue: cfg.DatabaseHostname},
				{SecretKey: "DB_PORT", SecretValue: fmt.Sprintf("%d", cfg.DatabasePort)},
			}

			_, err = infisicalClient.Secrets().Batch().Create(infisical.BatchCreateSecretsOptions{
				Environment: env,
				SecretPath:  secretPath,
				ProjectID:   cfg.InfisicalProjectId,
				Secrets:     secrets,
			})

			if err != nil {
				return fmt.Errorf("failed to create secrets in Infisical: %w", err)
			}
			fmt.Printf("secrets for app=%s created in Infisical at path=%s env=%s", provisionOptions.AppName, secretPath, env)
		}

		printProvisionSummary(os.Stdout, provisionOptions.AppName, provisionOptions.Database, provisionOptions.User, provisionOptions.Schema, provisionOptions.AppPassword, hidePassword)

		fmt.Printf("Provisioned database for app: %s\n", appName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(provisionCmd)

	provisionCmd.Flags().StringVar(&appPassword, "password", "", "Application database password (optional, random if empty)")
	provisionCmd.Flags().StringVar(&dbName, "dbname", "", "Override default generated database name (optional)")
	provisionCmd.Flags().StringVar(&userName, "user", "", "Override default generated database user (optional)")
	provisionCmd.Flags().StringVar(&schemaName, "schema", "", "Override default generated schema name (optional)")
	provisionCmd.Flags().BoolVar(&hidePassword, "hide-password", false, "Hide password in the summary output")
	provisionCmd.Flags().StringVar(&environment, "env", "dev", "Infisical environment to write secrets into")
}

func printProvisionSummary(w io.Writer, app, db, user, schema, password string, hide bool) {
	// TODO: find a better way to hide this password, possible length matching the length of the password
	if hide {
		password = "*****"
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Field\tValue")
	fmt.Fprintln(tw, "-----\t-----")
	fmt.Fprintf(tw, "App Name\t%s\n", app)
	fmt.Fprintf(tw, "Database\t%s\n", db)
	fmt.Fprintf(tw, "User\t%s\n", user)
	fmt.Fprintf(tw, "Schema\t%s\n", schema)
	fmt.Fprintf(tw, "Password\t%s\n", password)
	_ = tw.Flush()
}

// TODO: handle this better, maybe a bit stronger password?
func generateRandomPassword(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("pw-fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
