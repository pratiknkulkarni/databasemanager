package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	infisical "github.com/infisical/go-sdk"
	"github.com/spf13/cobra"

	"github.com/praaatik/databasemanager/internal/database"
)

// TODO: update these variable names for PROVISION_ instead. This is causing confusion being in same package.
var (
	provisionDatabasePassword string
	provisionDatabaseName     string
	provisionDatabaseHostname string
	provisionDatabasePort     int
	userName                  string
	schemaName                string
	hidePassword              bool
	environment               string
	dbType                    string
)

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision a new isolated application database",
	Long:  `Provision a new database, roles, and schema for an application.`,
	Args:  cobra.ExactArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if dbType == "" {
			dbType = "postgres"
		}

		// if --database postgres and no --port => port = 5432
		// if --database mysql and no --port => port = 3306
		// if no --database and --port <value> => port = <value>
		// if no --database and no --port => port = 5432 (default sets to postgres since no --database)

		portFlag := cmd.Flags().Lookup("port")
		portChanged := portFlag != nil && portFlag.Changed

		if !portChanged {
			switch dbType {
			case "postgres":
				provisionDatabasePort = 5432
			case "mysql":
				provisionDatabasePort = 3306
			default:
				provisionDatabasePort = 5432
			}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		logr.Debug("initiating provision command\n")

		appName := args[0]
		logr.Debug("setting application name to %s\n", appName)

		cfg, err := GetConfig(cmd)
		if err != nil {
			logr.Errorf("config could not be received: %v\n", err)
			return err
		}

		logr.Debug("config received successfully\n")

		client, err := initDatabaseClient(cfg, cmd)
		if err != nil {
			logr.Errorf("failed to get database client: %w\n", err)
			return err
		}

		if dbType != "postgres" && dbType != "mysql" {
			logr.Errorf("invalid database type %q, must be: postgres, mysql\n", dbType)
			return err
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if provisionDatabasePassword == "" {
			provisionDatabasePassword = generateRandomPassword(16)
			fmt.Fprintf(cmd.OutOrStdout(), "Generated random password for app: %s\n", appName)
			logr.Debugf("generated random password for app: %s\n", appName)
		}

		provisionOptions := database.ProvisionOptions{
			AppName:          args[0],
			DatabaseHostname: provisionDatabaseHostname,
			DatabasePassword: provisionDatabasePassword,
			DatabasePort:     provisionDatabasePort,
			DatabaseName:     provisionDatabaseName,
			DatabaseUser:     userName,
			Schema:           schemaName,
		}

		if provisionOptions.DatabaseName == "" {
			provisionOptions.DatabaseName = fmt.Sprintf("%s_db", provisionOptions.AppName)
			logr.Debugf("database name empty, setting default database name %s\n", provisionOptions.DatabaseName)
		}

		if provisionOptions.DatabaseUser == "" {
			provisionOptions.DatabaseUser = fmt.Sprintf("%s_user", provisionOptions.AppName)
			logr.Debugf("database user name empty, setting default database user name %s\n", provisionOptions.DatabaseUser)
		}

		if provisionOptions.Schema == "" {
			provisionOptions.Schema = fmt.Sprintf("%s_data", provisionOptions.AppName)
			logr.Debugf("database schema empty, setting default database schema %s\n", provisionOptions.Schema)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Provisioning database for app=%s\n", appName)

		if err := client.Provision(ctx, provisionOptions); err != nil {
			logr.Errorf("failed to provision database: %w\n", err)
			return err
		}

		//TODO: handle this error instead of ignoring it
		infisicalClient, err := GetInfisicalClient(cmd)
		if err != nil {
			logr.Errorf("unable to get the infisical client: %w\n", err)
			return err
		}

		if infisicalClient == nil {
			logr.Debug("no infisical client in the context\n")
		} else {
			cfg, err := GetConfig(cmd)
			if err != nil {
				logr.Errorf("failed to get config: %w\n", err)
				return err
			}

			env := "dev"
			if eFlag, _ := cmd.Flags().GetString("env"); eFlag != "" {
				logr.Debug("setting default environment to dev\n")
				env = eFlag
			}

			_, err = infisicalClient.Folders().Create(infisical.CreateFolderOptions{
				ProjectID:   cfg.InfisicalProjectId,
				Name:        provisionOptions.AppName,
				Environment: env,
			})

			if err != nil {
				logr.Errorf("failed to create folder in Infisical: %w\n", err)
				return err
			}

			secretPath := fmt.Sprintf("/%s", provisionOptions.AppName)
			logr.Debugf("setting secret path to %s\n", secretPath)

			secrets := []infisical.BatchCreateSecret{
				{SecretKey: "DB_TYPE", SecretValue: dbType},
				{SecretKey: "DB_NAME", SecretValue: provisionOptions.DatabaseName},
				{SecretKey: "DB_USER", SecretValue: provisionOptions.DatabaseUser},
				{SecretKey: "DB_PASSWORD", SecretValue: provisionOptions.DatabasePassword},
				{SecretKey: "DB_HOST", SecretValue: provisionOptions.DatabaseHostname},
				{SecretKey: "DB_PORT", SecretValue: fmt.Sprintf("%d", provisionOptions.DatabasePort)},
			}

			// no schema for mysql
			if dbType == "postgres" {
				logr.Debug("database type 'postgres' adding schema name\n")
				secrets = append(secrets, infisical.BatchCreateSecret{
					SecretKey:   "DB_SCHEMA",
					SecretValue: provisionOptions.Schema,
				})
			}

			_, err = infisicalClient.Secrets().Batch().Create(infisical.BatchCreateSecretsOptions{
				Environment: env,
				SecretPath:  secretPath,
				ProjectID:   cfg.InfisicalProjectId,
				Secrets:     secrets,
			})

			if err != nil {
				logr.Errorf("failed to create secrets in Infisical: %w\n", err)
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "secrets for app=%s created in Infisical at path=%s env=%s\n", provisionOptions.AppName, secretPath, env)
		}

		printProvisionSummary(os.Stdout, provisionOptions.AppName, provisionOptions.DatabaseName, provisionOptions.DatabaseUser, provisionOptions.Schema, provisionOptions.DatabasePassword, hidePassword)
		logr.Debug("provision summary printed successfully\n")

		fmt.Fprintf(cmd.OutOrStdout(), "Provisioned database for app: %s\n", appName)
		logr.Debug("Provisioned database for app: %s\n", appName)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(provisionCmd)

	provisionCmd.Flags().StringVar(&provisionDatabasePassword, "password", "", "Application database password (optional, random if empty)")
	provisionCmd.Flags().IntVar(&provisionDatabasePort, "port", 0, "Application database port (optional)")
	provisionCmd.Flags().StringVar(&provisionDatabaseName, "dbname", "", "Override default generated database name (optional)")
	provisionCmd.Flags().StringVar(&userName, "user", "", "Override default generated database user (optional)")
	provisionCmd.Flags().StringVar(&schemaName, "schema", "", "Override default generated schema name (optional)")
	provisionCmd.Flags().BoolVar(&hidePassword, "hide-password", false, "Hide password in the summary output")
	provisionCmd.Flags().StringVar(&environment, "env", "dev", "Infisical environment to write secrets into")

	provisionCmd.Flags().StringVar(&provisionDatabaseHostname, "hostname", "localhost", "DatabaseName hostname")
	provisionCmd.Flags().StringVarP(&dbType, "database", "d", "postgres", "DatabaseName type: postgres or mysql or any ol' database")
}

func printProvisionSummary(w io.Writer, app, db, user, schema, password string, hide bool) {
	// TODO: find a better way to hide this password, possible length matching the length of the password
	// TODO: update, I have this just need to wire it down here
	if hide {
		password = "*****"
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "Field\tValue")
	fmt.Fprintln(tw, "-----\t-----")
	fmt.Fprintf(tw, "App Name\t%s\n", app)
	fmt.Fprintf(tw, "DatabaseName\t%s\n", db)
	fmt.Fprintf(tw, "DatabaseHostname\t%s\n", provisionDatabaseHostname)
	fmt.Fprintf(tw, "DatabaseUser\t%s\n", user)
	fmt.Fprintf(tw, "Schema\t%s\n", schema)
	fmt.Fprintf(tw, "Password\t%s\n", password)
	fmt.Fprintf(tw, "Port\t%d\n", provisionDatabasePort)
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
