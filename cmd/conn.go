package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/atotto/clipboard"
	infisical "github.com/infisical/go-sdk"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"

	"github.com/praaatik/databasemanager/internal/config"
)

var (
	connEnv    string
	connFormat string
	connType   string
	connCopy   bool
)

var connCmd = &cobra.Command{
	Use:   "conn <app>",
	Short: "Get database connection string for an app",
	Long: `Generate connection strings for an app's database in various formats.

By default, displays all available formats in a table with masked passwords.
Use --format to output a specific format (unmasked) to stdout.
Use --copy with --format to copy the unmasked connection string to clipboard.

Examples:
  databasemanager conn myapp                          # Show all formats in table (masked)
  databasemanager conn myapp --format uri             # Print URI to stdout (unmasked)
  databasemanager conn myapp --copy --format uri      # Copy URI to clipboard (unmasked)
  databasemanager conn myapp --copy --format psql     # Copy psql command to clipboard`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		logger, err := GetLogger(cmd)
		if err != nil {
			return err
		}

		appName := args[0]

		//TODO: handle this error instead of ignoring it
		infClient, _ := GetInfisicalClient(cmd)
		cfg, err := GetConfig(cmd)
		if err != nil {
			logger.Debugf("config could not be received: %v\n", err)
			return err
		}

		if connCopy && connFormat == "table" {
			return fmt.Errorf("--copy requires --format flag. Available formats: uri, psql (postgres), mysql (mysql), env")
		}

		secretMap, err := fetchAppSecrets(infClient, cfg, appName, connEnv)
		if err != nil {
			return err
		}

		detectedType, err := detectDatabaseType(secretMap)
		if err != nil {
			return err
		}

		dbType := detectedType
		if connType != "" {
			if connType != detectedType {
				return fmt.Errorf("DB_TYPE mismatch: stored type is '%s' but --type flag specifies '%s'. Remove --type flag or use --type %s",
					detectedType, connType, detectedType)
			}
			dbType = connType
		} else if connFormat == "table" {
			fmt.Printf("Detected database type: %s\n\n", detectedType)
		}

		if err := validateFormatForDatabase(dbType, connFormat); err != nil {
			return err
		}

		if err := validateRequiredSecrets(secretMap, dbType); err != nil {
			return err
		}

		generator := &ConnectionStringGenerator{
			Host:     secretMap["DB_HOST"],
			Port:     secretMap["DB_PORT"],
			Database: secretMap["DB_NAME"],
			User:     secretMap["DB_USER"],
			Password: secretMap["DB_PASSWORD"],
			Schema:   secretMap["DB_SCHEMA"],
			DBType:   dbType,
		}

		if connFormat == "table" {
			formats, err := generator.GenerateAll()
			if err != nil {
				return err
			}
			return outputConnectionTable(formats, dbType)
		}

		connString, err := generator.Generate(connFormat)
		if err != nil {
			return err
		}

		if connCopy {
			//TODO: I need to test this somehow, disable clipboard
			if err := clipboard.WriteAll(connString); err != nil {
				fmt.Printf("Warning: Clipboard not available, printing to stdout instead:\n%s\n", connString)
				return nil
			}
			preview := maskPasswordMiddle(connString)
			fmt.Printf("%s copied to clipboard: %s\n", strings.ToUpper(connFormat), preview)
			return nil
		}

		fmt.Println(connString)
		return nil
	},
}

// ConnectionStringGenerator handles connection string generation for different databases
type ConnectionStringGenerator struct {
	Host     string
	Port     string
	Database string
	User     string
	Password string
	Schema   string
	DBType   string
}

// ConnectionFormat represents a connection string format
type ConnectionFormat struct {
	Name  string
	Value string
}

// Generate creates a connection string in the specified format
func (g *ConnectionStringGenerator) Generate(format string) (string, error) {
	if g.DBType == "postgres" {
		return g.GeneratePostgres(format)
	} else if g.DBType == "mysql" {
		return g.GenerateMySQL(format)
	}
	return "", fmt.Errorf("unsupported database type: %s", g.DBType)
}

// GeneratePostgres creates PostgreSQL connection strings
func (g *ConnectionStringGenerator) GeneratePostgres(format string) (string, error) {
	switch format {
	case "uri":
		if g.Schema != "" {
			return fmt.Sprintf("postgresql://%s:%s@%s:%s/%s?search_path=%s",
				g.User, g.Password, g.Host, g.Port, g.Database, g.Schema), nil
		}
		return fmt.Sprintf("postgresql://%s:%s@%s:%s/%s",
			g.User, g.Password, g.Host, g.Port, g.Database), nil

	case "psql":
		return fmt.Sprintf("PGPASSWORD=%s psql -h %s -p %s -d %s -U %s",
			g.Password, g.Host, g.Port, g.Database, g.User), nil

	case "env":
		if g.Schema != "" {
			return fmt.Sprintf("DATABASE_URL=postgresql://%s:%s@%s:%s/%s?search_path=%s",
				g.User, g.Password, g.Host, g.Port, g.Database, g.Schema), nil
		}
		return fmt.Sprintf("DATABASE_URL=postgresql://%s:%s@%s:%s/%s",
			g.User, g.Password, g.Host, g.Port, g.Database), nil

	default:
		return "", fmt.Errorf("unsupported format for postgres: %s", format)
	}
}

// GenerateMySQL creates MySQL connection strings
func (g *ConnectionStringGenerator) GenerateMySQL(format string) (string, error) {
	switch format {
	case "uri":
		return fmt.Sprintf("mysql://%s:%s@%s:%s/%s",
			g.User, g.Password, g.Host, g.Port, g.Database), nil

	case "mysql":
		return fmt.Sprintf("mysql -h %s -P %s -D %s -u %s -p%s",
			g.Host, g.Port, g.Database, g.User, g.Password), nil

	case "env":
		return fmt.Sprintf("DATABASE_URL=mysql://%s:%s@%s:%s/%s",
			g.User, g.Password, g.Host, g.Port, g.Database), nil

	default:
		return "", fmt.Errorf("unsupported format for mysql: %s", format)
	}
}

// GenerateAll generates all available connection string formats for the database type
func (g *ConnectionStringGenerator) GenerateAll() ([]ConnectionFormat, error) {
	var formats []ConnectionFormat

	if g.DBType == "postgres" {
		uri, _ := g.GeneratePostgres("uri")
		psql, _ := g.GeneratePostgres("psql")
		env, _ := g.GeneratePostgres("env")

		formats = append(formats,
			ConnectionFormat{Name: "URI", Value: uri},
			ConnectionFormat{Name: "PSQL Command", Value: psql},
			ConnectionFormat{Name: "Environment Variable", Value: env},
		)
	} else if g.DBType == "mysql" {
		uri, _ := g.GenerateMySQL("uri")
		mysql, _ := g.GenerateMySQL("mysql")
		env, _ := g.GenerateMySQL("env")

		formats = append(formats,
			ConnectionFormat{Name: "URI", Value: uri},
			ConnectionFormat{Name: "MySQL Command", Value: mysql},
			ConnectionFormat{Name: "Environment Variable", Value: env},
		)
	}

	return formats, nil
}

// fetchAppSecrets retrieves secrets from Infisical for the given app
func fetchAppSecrets(infClient infisical.InfisicalClientInterface, cfg *config.Config, appName, env string) (map[string]string, error) {
	secretPath := fmt.Sprintf("/%s", appName)

	secrets, err := infClient.Secrets().List(infisical.ListSecretsOptions{
		Environment: env,
		ProjectID:   cfg.InfisicalProjectID,
		SecretPath:  secretPath,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to fetch secrets for app %q: %w", appName, err)
	}

	if len(secrets) == 0 {
		return nil, fmt.Errorf("no secrets found for app %q in environment %q", appName, env)
	}

	secretMap := make(map[string]string)
	for _, s := range secrets {
		secretMap[s.SecretKey] = s.SecretValue
	}

	return secretMap, nil
}

// detectDatabaseType determines database type from DB_TYPE secret
func detectDatabaseType(secrets map[string]string) (string, error) {
	dbType, exists := secrets["DB_TYPE"]
	if !exists {
		return "", fmt.Errorf("DB_TYPE secret not found. This app may have been provisioned with an older version. Please re-provision the app")
	}

	if dbType != "postgres" && dbType != "mysql" {
		return "", fmt.Errorf("invalid DB_TYPE value: %q (must be 'postgres' or 'mysql')", dbType)
	}

	return dbType, nil
}

// validateRequiredSecrets checks if all required secrets are present
func validateRequiredSecrets(secrets map[string]string, dbType string) error {
	requiredKeys := []string{"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD"}

	var missing []string
	for _, key := range requiredKeys {
		if _, exists := secrets[key]; !exists {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("required secrets not found: %s", strings.Join(missing, ", "))
	}

	_, hasSchema := secrets["DB_SCHEMA"]

	if dbType == "postgres" && !hasSchema {
		return fmt.Errorf("DB_SCHEMA not found but DB_TYPE is 'postgres'. This indicates a database compatibility issue")
	}

	if dbType == "mysql" && hasSchema {
		return fmt.Errorf("DB_SCHEMA found but DB_TYPE is 'mysql'. This indicates a database compatibility issue")
	}

	return nil
}

// validateFormatForDatabase validates that the format is compatible with the database type
func validateFormatForDatabase(dbType, format string) error {
	if format == "table" {
		return nil
	}

	postgresFormats := map[string]bool{"uri": true, "psql": true, "env": true, "table": true}
	mysqlFormats := map[string]bool{"uri": true, "mysql": true, "env": true, "table": true}

	switch dbType {
	case "postgres":
		if !postgresFormats[format] {
			if format == "mysql" {
				return fmt.Errorf("format 'mysql' is not valid for postgres database. Available formats: uri, psql, env, table")
			}
			return fmt.Errorf("invalid format %q for postgres. Available formats: uri, psql, env, table", format)
		}

	case "mysql":
		if !mysqlFormats[format] {
			if format == "psql" {
				return fmt.Errorf("format 'psql' is not valid for mysql database. Available formats: uri, mysql, env, table")
			}
			return fmt.Errorf("invalid format %q for mysql. Available formats: uri, mysql, env, table", format)
		}

	default:
		return fmt.Errorf("unsupported database type: %s (must be 'postgres' or 'mysql')", dbType)
	}

	return nil
}

// outputConnectionTable displays connection strings in table format
func outputConnectionTable(formats []ConnectionFormat, dbType string) error {
	t := table.NewWriter()
	t.AppendHeader(table.Row{"Format", "Connection String"})

	for _, format := range formats {
		value := maskPassword(format.Value)
		t.AppendRow(table.Row{format.Name, value})
	}

	fmt.Println(t.Render())

	// adding this help tip in the bottom instead of a warning
	fmt.Println()
	if dbType == "postgres" {
		fmt.Println("Tip: Use --copy --format <uri|psql|env> to copy unmasked value to clipboard")
	} else if dbType == "mysql" {
		fmt.Println("Tip: Use --copy --format <uri|mysql|env> to copy unmasked value to clipboard")
	}

	return nil
}

// maskPassword masks the password in connection strings completely
func maskPassword(connString string) string {
	rePG := regexp.MustCompile(`(PGPASSWORD=)([^\s]+)`)
	connString = rePG.ReplaceAllString(connString, "${1}*****")

	reMySQL := regexp.MustCompile(`(-p)([^\s]+)`)
	connString = reMySQL.ReplaceAllString(connString, "${1}*****")

	re := regexp.MustCompile(`(://[^:]+:)([^@]+)(@)`)
	connString = re.ReplaceAllString(connString, "${1}*****${3}")

	return connString
}

// maskPasswordMiddle masks middle characters of password for preview
func maskPasswordMiddle(connString string) string {
	// PGPASSWORD=
	rePG := regexp.MustCompile(`(PGPASSWORD=)([^\s]+)`)
	connString = rePG.ReplaceAllStringFunc(connString, func(match string) string {
		parts := rePG.FindStringSubmatch(match)
		if len(parts) == 3 {
			password := parts[2]
			masked := maskPasswordString(password)
			return parts[1] + masked
		}
		return match
	})

	// -p<password> NO SPACE between p and password!
	reMySQL := regexp.MustCompile(`(-p)([^\s]+)`)
	connString = reMySQL.ReplaceAllStringFunc(connString, func(match string) string {
		parts := reMySQL.FindStringSubmatch(match)
		if len(parts) == 3 {
			password := parts[2]
			masked := maskPasswordString(password)
			return parts[1] + masked
		}
		return match
	})

	re := regexp.MustCompile(`(://[^:]+:)([^@]+)(@)`)
	connString = re.ReplaceAllStringFunc(connString, func(match string) string {
		parts := re.FindStringSubmatch(match)
		if len(parts) == 4 {
			password := parts[2]
			masked := maskPasswordString(password)
			return parts[1] + masked + parts[3]
		}
		return match
	})

	return connString
}

// maskPasswordString should mask the password except for the first two characters and last two characters.
func maskPasswordString(password string) string {
	if len(password) <= 4 {
		return "***"
	}
	return password[:2] + "***" + password[len(password)-2:]
}

func init() {
	rootCmd.AddCommand(connCmd)

	connCmd.Flags().StringVar(&connEnv, "env", "dev", "Infisical environment")
	connCmd.Flags().StringVar(&connFormat, "format", "table", "Output format: table, uri, psql (postgres), mysql (mysql), env")
	connCmd.Flags().StringVar(&connType, "type", "", "Database type override (validates against stored DB_TYPE): postgres, mysql")
	connCmd.Flags().BoolVar(&connCopy, "copy", false, "Copy connection string to clipboard (requires --format)")
}
