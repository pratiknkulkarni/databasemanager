package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
)

// ProvisionerDeps are the explicit dependencies of the Provisioner. Secrets
// may be nil, in which case provisioning proceeds without syncing to
// Infisical and the result reports Synced=false.
type ProvisionerDeps struct {
	DB        database.Database
	Secrets   infisical.InfisicalClientInterface
	Logger    *log.Logger
	ProjectID string
	Engine    string                // database.EnginePostgres or database.EngineMySQL
	EngineCfg config.DatabaseConfig // the section the adapter dials with; source of recorded host/port
}

// Provisioner owns the database + secrets lifecycle for an app: provisioning
// with credential sync, and deprovisioning with secret cleanup.
type Provisioner struct {
	deps ProvisionerDeps
}

func NewProvisioner(deps ProvisionerDeps) *Provisioner {
	return &Provisioner{deps: deps}
}

type ProvisionRequest struct {
	AppName     string
	Environment string // dev, prod, etc

	// Overrides
	DBName     string
	DBUser     string
	DBPassword string // Optional, will generate if empty
	DBSchema   string // Postgres only
	Port       int
	Host       string
}

// ProvisionResult reports what was provisioned and whether the credentials
// were actually synced to Infisical. When Synced is false the caller owns the
// only copy of Credentials — the generated password exists nowhere else.
type ProvisionResult struct {
	Options     database.ProvisionOptions
	Credentials database.Credentials
	Synced      bool
}

// Run provisions the database and syncs its credentials to Infisical.
func (p *Provisioner) Run(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error) {
	p.deps.Logger.Info("starting provisioning", "app", req.AppName, "type", p.deps.Engine)

	opts, err := p.prepareOptions(req)
	if err != nil {
		return nil, err
	}

	p.deps.Logger.Info("provisioning database", "name", opts.DatabaseName, "user", opts.DatabaseUser)
	if err := p.deps.DB.Provision(ctx, *opts); err != nil {
		return nil, fmt.Errorf("database provisioning failed: %w", err)
	}

	result := &ProvisionResult{Options: *opts, Credentials: p.credentials(*opts)}

	if p.deps.Secrets == nil {
		p.deps.Logger.Warn("skipping infisical sync: client not initialized")
		return result, nil
	}

	if err := p.syncToInfisical(req, *opts); err != nil {
		p.deps.Logger.Error("database was created but its secrets were not synced", "app", req.AppName, "err", err)
		return nil, fmt.Errorf("infisical sync failed: %w", err)
	}
	result.Synced = true

	return result, nil
}

// sanitizeAppName maps an app name onto the SQL identifier charset.
func sanitizeAppName(appName string) string {
	return strings.ReplaceAll(appName, "-", "_")
}

// prepareOptions fills in defaults for anything the request does not
// override. Recorded connection facts (host, port) come from the same engine
// configuration the adapter dials with — never from hardcoded defaults.
func (p *Provisioner) prepareOptions(req ProvisionRequest) (*database.ProvisionOptions, error) {
	safeAppName := sanitizeAppName(req.AppName)

	password := req.DBPassword
	if password == "" {
		var err error
		password, err = generateRandomPassword(16)
		if err != nil {
			return nil, err
		}
	}

	dbName := req.DBName
	if dbName == "" {
		dbName = fmt.Sprintf("%s_db", safeAppName)
	}

	dbUser := req.DBUser
	if dbUser == "" {
		dbUser = fmt.Sprintf("%s_user", safeAppName)
	}

	schema := req.DBSchema
	if p.deps.Engine == database.EnginePostgres && schema == "" {
		schema = "public"
	}
	// A schema on a non-postgres engine would record a DB_SCHEMA secret that
	// every reader of the contract rejects for mysql — refuse it up front.
	if p.deps.Engine != database.EnginePostgres && schema != "" {
		return nil, fmt.Errorf("--schema is only supported for postgres (got engine %q)", p.deps.Engine)
	}

	port := req.Port
	if port == 0 {
		port = p.deps.EngineCfg.DatabasePort
	}

	host := req.Host
	if host == "" {
		host = p.deps.EngineCfg.DatabaseHostname
	}

	return &database.ProvisionOptions{
		AppName:          req.AppName,
		DatabaseName:     dbName,
		DatabaseUser:     dbUser,
		DatabasePassword: password,
		DatabasePort:     port,
		DatabaseHostname: host,
		Schema:           schema, // Empty for MySQL
	}, nil
}

// credentials assembles the secret contract for the provisioned database.
func (p *Provisioner) credentials(opts database.ProvisionOptions) database.Credentials {
	return database.Credentials{
		Type:     p.deps.Engine,
		Host:     opts.DatabaseHostname,
		Port:     strconv.Itoa(opts.DatabasePort),
		Name:     opts.DatabaseName,
		User:     opts.DatabaseUser,
		Password: opts.DatabasePassword,
		Schema:   opts.Schema,
	}
}

func (p *Provisioner) syncToInfisical(req ProvisionRequest, opts database.ProvisionOptions) error {
	p.deps.Logger.Info("syncing secrets to infisical", "env", req.Environment)

	if err := p.ensureFolder(req.AppName, req.Environment); err != nil {
		return err
	}

	secretPath := fmt.Sprintf("/%s", req.AppName)

	var secrets []infisical.BatchCreateSecret
	for _, kv := range p.credentials(opts).ToSecrets() {
		secrets = append(secrets, infisical.BatchCreateSecret{
			SecretKey:   kv.Key,
			SecretValue: kv.Value,
		})
	}

	_, err := p.deps.Secrets.Secrets().Batch().Create(infisical.BatchCreateSecretsOptions{
		ProjectID:   p.deps.ProjectID,
		Environment: req.Environment,
		SecretPath:  secretPath,
		Secrets:     secrets,
	})

	return err
}

// ensureFolder creates the app folder. A creation failure is only tolerated
// when the folder verifiably already exists; anything else fails the sync.
func (p *Provisioner) ensureFolder(appName, environment string) error {
	_, err := p.deps.Secrets.Folders().Create(infisical.CreateFolderOptions{
		ProjectID:   p.deps.ProjectID,
		Name:        appName,
		Environment: environment,
	})
	if err == nil {
		return nil
	}

	folders, listErr := p.deps.Secrets.Folders().List(infisical.ListFoldersOptions{
		ProjectID:   p.deps.ProjectID,
		Environment: environment,
	})
	if listErr == nil {
		for _, folder := range folders {
			if folder.Name == appName {
				p.deps.Logger.Debug("infisical folder already exists", "app", appName)
				return nil
			}
		}
	}

	return fmt.Errorf("failed to create infisical folder %q: %w", appName, err)
}

// ResolveApp reads the app's recorded credentials from Infisical — the source
// of truth for what was provisioned.
func (p *Provisioner) ResolveApp(appName, environment string) (database.Credentials, error) {
	if p.deps.Secrets == nil {
		return database.Credentials{}, fmt.Errorf("infisical client not initialized")
	}

	secretPath := fmt.Sprintf("/%s", appName)
	secrets, err := p.deps.Secrets.Secrets().List(infisical.ListSecretsOptions{
		Environment: environment,
		ProjectID:   p.deps.ProjectID,
		SecretPath:  secretPath,
	})
	if err != nil {
		return database.Credentials{}, fmt.Errorf("failed to fetch secrets for app %q: %w", appName, err)
	}
	if len(secrets) == 0 {
		return database.Credentials{}, fmt.Errorf("app %q not found in environment %q (no secrets found)", appName, environment)
	}

	secretMap := make(map[string]string, len(secrets))
	for _, s := range secrets {
		secretMap[s.SecretKey] = s.SecretValue
	}

	creds := database.ParseCredentials(secretMap)
	if creds.Name == "" || creds.User == "" {
		return database.Credentials{}, fmt.Errorf("incomplete secrets for app %q: missing DB_NAME or DB_USER. Unable to delete database without it", appName)
	}

	return creds, nil
}

// Deprovision drops the database and user named by the resolved credentials,
// then removes the app's secrets and folder from Infisical. The database drop
// happens first; if it fails, the secrets remain so the app stays resolvable.
func (p *Provisioner) Deprovision(ctx context.Context, appName, environment string, creds database.Credentials) error {
	if p.deps.Secrets == nil {
		return fmt.Errorf("infisical client not initialized")
	}

	if err := p.deps.DB.Delete(ctx, creds.Name, creds.User); err != nil {
		return fmt.Errorf("failed to delete database: %w", err)
	}

	secretPath := fmt.Sprintf("/%s", appName)
	secrets, err := p.deps.Secrets.Secrets().List(infisical.ListSecretsOptions{
		Environment: environment,
		ProjectID:   p.deps.ProjectID,
		SecretPath:  secretPath,
	})
	if err != nil {
		return fmt.Errorf("database deleted, but failed to list secrets for cleanup: %w", err)
	}

	for _, secret := range secrets {
		_, err := p.deps.Secrets.Secrets().Delete(infisical.DeleteSecretOptions{
			Environment: environment,
			ProjectID:   p.deps.ProjectID,
			SecretPath:  secretPath,
			SecretKey:   secret.SecretKey,
		})
		if err != nil {
			return fmt.Errorf("database deleted, but failed to delete secret %q: %w", secret.SecretKey, err)
		}
	}

	_, err = p.deps.Secrets.Folders().Delete(infisical.DeleteFolderOptions{
		FolderName:  appName,
		ProjectID:   p.deps.ProjectID,
		Environment: environment,
		Path:        "/",
	})
	if err != nil {
		return fmt.Errorf("database deleted, but failed to delete app folder from Infisical: %w", err)
	}

	return nil
}
