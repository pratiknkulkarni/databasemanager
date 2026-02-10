package services

import (
	"context"
	"fmt"
	"strings"

	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/database"
	"github.com/praaatik/databasemanager/internal/utils"
)

type Provisioner struct {
	App *app.Container
}

func NewProvisioner(app *app.Container) *Provisioner {
	return &Provisioner{App: app}
}

type ProvisionRequest struct {
	AppName     string
	Type        string // postgres or mysql (or something else can be added)
	Environment string // dev, prod, etc

	// Overrides
	DBName     string
	DBUser     string
	DBPassword string // Optional, will generate if empty
	DBSchema   string // Postgres only
	Port       int
}

// Run actually runs the provision command
func (p *Provisioner) Run(ctx context.Context, req ProvisionRequest) (*database.ProvisionOptions, error) {
	p.App.Logger.Info("starting provisioning", "app", req.AppName, "type", req.Type)

	opts, err := p.prepareOptions(req)
	if err != nil {
		return nil, err
	}

	p.App.Logger.Info("provisioning database", "name", opts.DatabaseName, "user", opts.DatabaseUser)
	if err := p.App.DB.Provision(ctx, *opts); err != nil {
		return nil, fmt.Errorf("database provisioning failed: %w", err)
	}

	if p.App.Infisical != nil {
		if err := p.syncToInfisical(req, *opts); err != nil {
			p.App.Logger.Error("failed to sync secrets", "err", err)
			return nil, fmt.Errorf("infisical sync failed: %w", err)
		}
	} else {
		p.App.Logger.Warn("skipping infisical sync: client not initialized")
	}

	return opts, nil
}

// prepareOptions prepares the defaults for the databases if the user does not pass them
func (p *Provisioner) prepareOptions(req ProvisionRequest) (*database.ProvisionOptions, error) {
	safeAppName := strings.ReplaceAll(req.AppName, "-", "_")

	password := req.DBPassword
	if password == "" {
		var err error
		password, err = utils.GenerateRandomPassword(16)
		if err != nil {
			return nil, err
		}
	}

	dbName := req.DBName
	if dbName == "" {
		dbName = fmt.Sprintf("%s_db", safeAppName)
	}

	schema := req.DBSchema
	if req.Type == "postgres" && schema == "" {
		schema = "public"
	}

	dbUser := req.DBUser
	if dbUser == "" {
		dbUser = fmt.Sprintf("%s_user", req.AppName)
	}

	port := req.Port
	if port == 0 {
		if req.Type == "mysql" {
			port = 3306
		} else {
			port = 5432
		}
	}

	host := "localhost"
	if req.Type == "mysql" {
		host = p.App.Config.MySQL.DatabaseHostname
	} else {
		host = p.App.Config.Postgres.DatabaseHostname
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

func (p *Provisioner) syncToInfisical(req ProvisionRequest, opts database.ProvisionOptions) error {
	p.App.Logger.Info("syncing secrets to infisical", "env", req.Environment)

	_, err := p.App.Infisical.Folders().Create(infisical.CreateFolderOptions{
		ProjectID:   p.App.Config.InfisicalProjectID,
		Name:        req.AppName,
		Environment: req.Environment,
	})
	if err != nil {
		p.App.Logger.Debug("folder creation response", "err", err)
	}

	secretPath := fmt.Sprintf("/%s", req.AppName)

	secrets := []infisical.BatchCreateSecret{
		{SecretKey: "DB_TYPE", SecretValue: req.Type},
		{SecretKey: "DB_NAME", SecretValue: opts.DatabaseName},
		{SecretKey: "DB_USER", SecretValue: opts.DatabaseUser},
		{SecretKey: "DB_PASSWORD", SecretValue: opts.DatabasePassword},
		{SecretKey: "DB_HOST", SecretValue: opts.DatabaseHostname},
		{SecretKey: "DB_PORT", SecretValue: fmt.Sprintf("%d", opts.DatabasePort)},
	}

	if req.Type == "postgres" && opts.Schema != "" {
		secrets = append(secrets, infisical.BatchCreateSecret{
			SecretKey:   "DB_SCHEMA",
			SecretValue: opts.Schema,
		})
	}

	_, err = p.App.Infisical.Secrets().Batch().Create(infisical.BatchCreateSecretsOptions{
		ProjectID:   p.App.Config.InfisicalProjectID,
		Environment: req.Environment,
		SecretPath:  secretPath,
		Secrets:     secrets,
	})

	return err
}
