package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/atotto/clipboard"
	infisical "github.com/infisical/go-sdk"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
	"github.com/spf13/cobra"
)

func newConnCmd(container *app.Container) *cobra.Command {
	var (
		connEnv    string
		connFormat string
		connType   string
		connCopy   bool
	)

	cmd := &cobra.Command{
		Use:   "conn <app>",
		Short: "Get database connection string for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]

			if container.Infisical == nil {
				return fmt.Errorf("infisical client not initialized: cannot fetch secrets")
			}

			if connCopy && connFormat == "table" {
				return fmt.Errorf("--copy requires --format flag. Available formats: uri, psql (postgres), mysql (mysql), env")
			}

			creds, err := fetchAppCredentials(container.Infisical, container.Config, appName, connEnv)
			if err != nil {
				return err
			}

			detectedType, err := creds.ResolveType()
			if err != nil {
				return err
			}

			dbType := detectedType
			if connType != "" {
				if connType != detectedType {
					return fmt.Errorf("DB_TYPE mismatch: stored type is '%s' but --type flag specifies '%s'",
						detectedType, connType)
				}
				dbType = connType
			} else if connFormat == "table" {
				container.Logger.Info("Detected database type", "type", detectedType)
			}

			if err := validateFormatForDatabase(dbType, connFormat); err != nil {
				return err
			}

			if err := creds.ValidateContract(dbType); err != nil {
				return err
			}

			generator := &ConnectionStringGenerator{
				Host:     creds.Host,
				Port:     creds.Port,
				Database: creds.Name,
				User:     creds.User,
				Password: creds.Password,
				Schema:   creds.Schema,
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
				if err := clipboard.WriteAll(connString); err != nil {
					container.Logger.Warn("Clipboard not available, printing to stdout instead")
					fmt.Println(connString)
					return nil
				}
				preview := maskPasswordMiddle(connString)
				container.Logger.Info("Copied to clipboard", "format", strings.ToUpper(connFormat), "preview", preview)
				return nil
			}

			fmt.Println(connString)
			return nil
		},
	}

	cmd.Flags().StringVar(&connEnv, "env", "dev", "Infisical environment")
	cmd.Flags().StringVar(&connFormat, "format", "table", "Output format: table, uri, psql (postgres), mysql (mysql), env")
	cmd.Flags().StringVar(&connType, "type", "", "Database type override (validates against stored DB_TYPE): postgres, mysql")
	cmd.Flags().BoolVar(&connCopy, "copy", false, "Copy connection string to clipboard (requires --format)")

	return cmd
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
	switch g.DBType {
	case "postgres":
		return g.GeneratePostgres(format)
	case "mysql":
		return g.GenerateMySQL(format)
	default:
		return "", fmt.Errorf("unsupported database type: %s", g.DBType)
	}
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
	var specs []struct{ name, format string }

	switch g.DBType {
	case "postgres":
		specs = []struct{ name, format string }{
			{"URI", "uri"},
			{"PSQL Command", "psql"},
			{"Environment Variable", "env"},
		}
	case "mysql":
		specs = []struct{ name, format string }{
			{"URI", "uri"},
			{"MySQL Command", "mysql"},
			{"Environment Variable", "env"},
		}
	default:
		return nil, fmt.Errorf("unsupported database type: %s", g.DBType)
	}

	formats := make([]ConnectionFormat, 0, len(specs))
	for _, spec := range specs {
		value, err := g.Generate(spec.format)
		if err != nil {
			return nil, err
		}
		formats = append(formats, ConnectionFormat{Name: spec.name, Value: value})
	}

	return formats, nil
}

// fetchAppCredentials retrieves an app's recorded credentials from Infisical.
func fetchAppCredentials(infClient infisical.InfisicalClientInterface, cfg *config.Config, appName, env string) (database.Credentials, error) {
	secretPath := fmt.Sprintf("/%s", appName)

	secrets, err := infClient.Secrets().List(infisical.ListSecretsOptions{
		Environment: env,
		ProjectID:   cfg.InfisicalProjectID,
		SecretPath:  secretPath,
	})

	if err != nil {
		return database.Credentials{}, fmt.Errorf("failed to fetch secrets for app %q: %w", appName, err)
	}

	if len(secrets) == 0 {
		return database.Credentials{}, fmt.Errorf("no secrets found for app %q in environment %q", appName, env)
	}

	secretMap := make(map[string]string, len(secrets))
	for _, s := range secrets {
		secretMap[s.SecretKey] = s.SecretValue
	}

	return database.ParseCredentials(secretMap), nil
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
	switch dbType {
	case "postgres":
		fmt.Println("Tip: Use --copy --format <uri|psql|env> to copy unmasked value to clipboard")
	case "mysql":
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
