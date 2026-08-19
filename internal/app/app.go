package app

import (
	"github.com/charmbracelet/log"
	infisical "github.com/infisical/go-sdk"
	"github.com/pratiknkulkarni/databasemanager/internal/config"
)

// Container holds the process-wide dependencies wired at the composition
// root. It is populated once (root command PersistentPreRunE) and must not be
// mutated afterwards; per-invocation dependencies such as the database engine
// adapter are constructed inside the command that needs them.
type Container struct {
	Config    *config.Config
	Logger    *log.Logger
	Infisical infisical.InfisicalClientInterface
}
