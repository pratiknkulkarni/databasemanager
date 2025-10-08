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

	"github.com/spf13/cobra"

	"github.com/praaatik/databasemanager/internal/database"
)

var (
	appName      string
	appPassword  string
	dbName       string
	userName     string
	schemaName   string
	hidePassword bool
)

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision a new isolated application database",
	Long:  `Provision a new database, roles, and schema for an application.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if appName == "" {
			return fmt.Errorf("--app are required")
		}

		client := GetDatabaseClient(cmd)
		// if client == nil {
		// 	return fmt.Errorf("database client not present in the context")
		// }

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if appPassword == "" {
			appPassword = generateRandomPassword(16)
			log.Printf("Generated random password for app=%s", appName)
		}

		provisionOptions := database.ProvisionOptions{
			AppName:     appName,
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

		printProvisionSummary(os.Stdout, provisionOptions.AppName, provisionOptions.Database, provisionOptions.User, provisionOptions.Schema, provisionOptions.AppPassword, hidePassword)

		log.Printf("Provisioned database for app=%s", appName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(provisionCmd)

	provisionCmd.Flags().StringVar(&appName, "app", "", "Application name (required)")
	provisionCmd.Flags().StringVar(&appPassword, "password", "", "Application database password (optional, random if empty)")
	provisionCmd.Flags().StringVar(&dbName, "dbname", "", "Override default generated database name (optional)")
	provisionCmd.Flags().StringVar(&userName, "user", "", "Override default generated database user (optional)")
	provisionCmd.Flags().StringVar(&schemaName, "schema", "", "Override default generated schema name (optional)")
	provisionCmd.Flags().BoolVar(&hidePassword, "hide-password", false, "Hide password in the summary output")

	_ = provisionCmd.MarkFlagRequired("app")
}

func printProvisionSummary(w io.Writer, app, db, user, schema, password string, hide bool) {
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

func generateRandomPassword(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("pw-fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
