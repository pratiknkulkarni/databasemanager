package services

import (
	"context"

	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/database"
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

// TODO: implement this
func (p *Provisioner) Run(ctx context.Context, req ProvisionRequest) (*database.ProvisionOptions, error) {
	return nil, nil
}

// TODO: implement this
func (p *Provisioner) prepareOptions(req ProvisionRequest) (*database.ProvisionOptions, error) {
	return nil, nil
}

// TODO: implement this
func (p *Provisioner) syncToInfisical(req ProvisionRequest, opts database.ProvisionOptions) error {
	return nil
}
