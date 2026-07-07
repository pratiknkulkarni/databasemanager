package services

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
)

// rollbackTimeout bounds cleanup work that must survive a cancelled request
// context (provision rollback, rotation rollback).
const rollbackTimeout = 10 * time.Second

// ProvisionerDeps are the explicit dependencies of the Provisioner. Secrets
// may be nil, in which case provisioning proceeds without syncing to
// Infisical and the result reports Synced=false.
type ProvisionerDeps struct {
	DB        database.Database
	Secrets   SecretStore
	Logger    *log.Logger
	Engine    string                // database.EnginePostgres or database.EngineMySQL
	EngineCfg config.DatabaseConfig // the section the adapter dials with; source of recorded host/port
}

// Provisioner owns the database + secrets lifecycle for an app: provisioning
// with credential sync, deprovisioning with secret cleanup, and password
// rotation.
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

// secretPath returns the Infisical folder path for an app.
func secretPath(appName string) string {
	return "/" + appName
}

// Run provisions the database and syncs its credentials to Infisical.
//
// When the database is provisioned but the secret sync fails, Run returns a
// non-nil result TOGETHER with the error: the result carries the only copy of
// the generated credentials, and the caller must surface them or they are
// lost (the database exists, but nothing records its password).
func (p *Provisioner) Run(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error) {
	p.deps.Logger.Info("starting provisioning", "app", req.AppName, "type", p.deps.Engine)

	opts, err := p.prepareOptions(req)
	if err != nil {
		return nil, err
	}

	// Pre-flight the secret store BEFORE touching the database: a wrong
	// environment slug or an already-recorded app must fail while there is
	// still nothing to clean up.
	var existing map[string]string
	if p.deps.Secrets != nil {
		existing, err = p.prepareSecretSync(req.AppName, req.Environment)
		if err != nil {
			return nil, err
		}
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

	p.deps.Logger.Info("syncing secrets to infisical", "env", req.Environment)
	plan := planSecretSync(existing, result.Credentials.ToSecrets())
	if err := p.executeSyncPlan(req.Environment, secretPath(req.AppName), plan); err != nil {
		p.deps.Logger.Error("database was created but its secrets were not synced", "app", req.AppName, "err", err)
		// The database exists and the generated password is recorded nowhere
		// else: return the result alongside the error so the caller can
		// surface the credentials instead of losing them.
		return result, fmt.Errorf("infisical sync failed: %w", err)
	}
	result.Synced = true

	return result, nil
}

// prepareSecretSync verifies the secret store is ready to record the app
// before any database mutation: the folder exists (fail-closed creation) and
// the app is not already recorded. It returns the secrets currently stored at
// the app's path for reconciliation planning.
func (p *Provisioner) prepareSecretSync(appName, environment string) (map[string]string, error) {
	if err := p.ensureFolder(appName, environment); err != nil {
		return nil, err
	}

	existing, err := p.deps.Secrets.ListSecrets(environment, secretPath(appName))
	if err != nil {
		return nil, fmt.Errorf("failed to inspect existing secrets for app %q: %w", appName, err)
	}

	for _, key := range database.ContractKeys() {
		if _, ok := existing[key]; ok {
			return nil, fmt.Errorf("app %q already has recorded secrets in environment %q (%s present); delete the app first", appName, environment, key)
		}
	}

	return existing, nil
}

// executeSyncPlan applies a reconciliation plan: creates, then updates, then
// deletions of stale contract keys — each slice in the contract's
// deterministic order, so a partial failure names exactly the key that did
// not land and a re-run picks up where it stopped.
func (p *Provisioner) executeSyncPlan(environment, path string, plan syncPlan) error {
	for _, kv := range plan.creates {
		if err := p.deps.Secrets.CreateSecret(environment, path, kv); err != nil {
			return fmt.Errorf("failed to create secret %q: %w", kv.Key, err)
		}
	}
	for _, kv := range plan.updates {
		if err := p.deps.Secrets.UpdateSecret(environment, path, kv); err != nil {
			return fmt.Errorf("failed to update secret %q: %w", kv.Key, err)
		}
	}
	for _, key := range plan.deletes {
		if err := p.deps.Secrets.DeleteSecret(environment, path, key); err != nil {
			return fmt.Errorf("failed to delete stale secret %q: %w", key, err)
		}
	}
	return nil
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

// ensureFolder creates the app folder. A creation failure is only tolerated
// when the folder verifiably already exists; anything else fails the sync.
func (p *Provisioner) ensureFolder(appName, environment string) error {
	err := p.deps.Secrets.CreateFolder(environment, appName)
	if err == nil {
		return nil
	}

	exists, listErr := p.deps.Secrets.FolderExists(environment, appName)
	if listErr == nil && exists {
		p.deps.Logger.Debug("infisical folder already exists", "app", appName)
		return nil
	}

	return fmt.Errorf("failed to create infisical folder %q: %w", appName, err)
}

// ResolveApp reads the app's recorded credentials from Infisical — the source
// of truth for what was provisioned.
func (p *Provisioner) ResolveApp(appName, environment string) (database.Credentials, error) {
	if p.deps.Secrets == nil {
		return database.Credentials{}, fmt.Errorf("infisical client not initialized")
	}

	secrets, err := p.deps.Secrets.ListSecrets(environment, secretPath(appName))
	if err != nil {
		return database.Credentials{}, fmt.Errorf("failed to fetch secrets for app %q: %w", appName, err)
	}
	if len(secrets) == 0 {
		return database.Credentials{}, fmt.Errorf("app %q not found in environment %q (no secrets found)", appName, environment)
	}

	creds := database.ParseCredentials(secrets)
	if creds.Name == "" || creds.User == "" {
		return database.Credentials{}, fmt.Errorf("incomplete secrets for app %q: missing DB_NAME or DB_USER. Unable to delete database without it", appName)
	}

	return creds, nil
}

// RotateResult reports the outcome of a password rotation.
type RotateResult struct {
	// Credentials carry the NEW password. When RolledBack is true the
	// database was restored to the old password and the new one is dead —
	// do not surface it.
	Credentials database.Credentials
	Synced      bool // the DB_PASSWORD update landed in Infisical
	RolledBack  bool // sync failed and the database was restored to the old password
}

// Rotate issues a new password for the app's database user and records it in
// Infisical. The database changes first; if the secret update then fails, the
// old password is restored so a failed rotation leaves nothing changed. When
// even that rollback fails, the returned result accompanies the error and
// carries the new password — the caller owns the only copy and must surface
// it.
func (p *Provisioner) Rotate(ctx context.Context, appName, environment, overridePassword string) (*RotateResult, error) {
	if p.deps.Secrets == nil {
		return nil, fmt.Errorf("infisical client not initialized")
	}

	creds, err := p.ResolveApp(appName, environment)
	if err != nil {
		return nil, err
	}
	// Legacy apps predate DB_TYPE; the caller resolved the engine (possibly
	// via --type), so backfill it for downstream consumers such as the
	// post-rotation connectivity check.
	if creds.Type == "" {
		creds.Type = p.deps.Engine
	}

	newPassword := overridePassword
	if newPassword == "" {
		newPassword, err = generateRandomPassword(16)
		if err != nil {
			return nil, err
		}
	}

	p.deps.Logger.Info("rotating password", "app", appName, "user", creds.User)

	if err := p.deps.DB.RotatePassword(ctx, creds.User, newPassword); err != nil {
		return nil, fmt.Errorf("failed to apply new password: %w", err)
	}
	p.deps.Logger.Info("password applied to database user", "user", creds.User)

	oldPassword := creds.Password
	creds.Password = newPassword
	result := &RotateResult{Credentials: creds}

	kv := database.SecretKV{Key: database.SecretKeyPassword, Value: newPassword}
	// Update in place; a missing DB_PASSWORD secret (the lost-password
	// recovery case) is created instead.
	var syncErr error
	if oldPassword == "" {
		syncErr = p.deps.Secrets.CreateSecret(environment, secretPath(appName), kv)
	} else {
		syncErr = p.deps.Secrets.UpdateSecret(environment, secretPath(appName), kv)
	}
	if syncErr == nil {
		result.Synced = true
		p.deps.Logger.Info("DB_PASSWORD updated in Infisical", "app", appName)
		return result, nil
	}

	p.deps.Logger.Error("new password was applied to the database but not recorded in Infisical",
		"app", appName, "err", syncErr)

	if oldPassword != "" {
		// Restore the recorded password so a failed rotation changes nothing.
		// Fresh context: the request context may already be cancelled.
		rollbackCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		if rbErr := p.deps.DB.RotatePassword(rollbackCtx, creds.User, oldPassword); rbErr != nil {
			p.deps.Logger.Error("rollback to the previous password also failed", "err", rbErr)
		} else {
			result.RolledBack = true
			return result, fmt.Errorf("infisical update failed (database password restored, nothing changed): %w", syncErr)
		}
	}

	// The new password is live on the database and recorded nowhere else.
	return result, fmt.Errorf("infisical update failed and the database keeps the new password: %w", syncErr)
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

	path := secretPath(appName)
	secrets, err := p.deps.Secrets.ListSecrets(environment, path)
	if err != nil {
		return fmt.Errorf("database deleted, but failed to list secrets for cleanup: %w", err)
	}

	// Sorted for a deterministic deletion order (and stable partial-failure
	// error messages).
	for _, key := range slices.Sorted(maps.Keys(secrets)) {
		if err := p.deps.Secrets.DeleteSecret(environment, path, key); err != nil {
			return fmt.Errorf("database deleted, but failed to delete secret %q: %w", key, err)
		}
	}

	if err := p.deps.Secrets.DeleteFolder(environment, appName); err != nil {
		return fmt.Errorf("database deleted, but failed to delete app folder from Infisical: %w", err)
	}

	return nil
}
