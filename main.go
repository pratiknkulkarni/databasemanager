package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/praaatik/databasemanager/cmd"
	"github.com/praaatik/databasemanager/internal/app"
	"github.com/praaatik/databasemanager/internal/logger"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	l := logger.New(logger.LogConfig{Level: log.InfoLevel})

	container := &app.Container{Logger: l}

	rootCmd := cmd.NewRootCmd(container)
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		stop()
		os.Exit(1)
	}
}
