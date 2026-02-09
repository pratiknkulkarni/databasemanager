package main

import (
	"os"

	"github.com/charmbracelet/log"
	"github.com/praaatik/databasemanager/cmd"
	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/config"
	"github.com/praaatik/databasemanager/internal/infisical"
	"github.com/praaatik/databasemanager/internal/logger"
)

func main() {
	//cmd.Execute()

	l := logger.New(logger.LogConfig{Level: log.InfoLevel})

	cfgPath := ""
	for i, arg := range os.Args {
		if arg == "--config" && i+1 < len(os.Args) {
			cfgPath = os.Args[i+1]
			break
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		l.Fatal("failed to load configuration", "err", err)
	}

	infisicalClient, err := infisical.NewClient(cfg)
	// no crashes here, maybe it takes from a local instead of db? I could add this later on
	if err != nil {
		l.Warn("failed to initialize infisical client", "err", err)
	}

	container := &app.Container{
		Config:    cfg,
		Logger:    l,
		Infisical: infisicalClient,
	}

	rootCmd := cmd.NewRootCmd(container)
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
