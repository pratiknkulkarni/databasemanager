package services

import "github.com/praaatik/databasemanager/internal/app"

type Provisioner struct {
	App *app.Container
}

func NewProvisioner(app *app.Container) *Provisioner {
	return &Provisioner{App: app}
}
