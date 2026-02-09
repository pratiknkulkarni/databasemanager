package app

import (
	"github.com/charmbracelet/log"
	infisical "github.com/infisical/go-sdk"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/database"
)

// Container holds the application's dependencies.
type Container struct {
	Config    *config.Config
	Logger    *log.Logger
	DB        database.Database
	Infisical infisical.InfisicalClientInterface
}
