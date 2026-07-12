package main

import (
	"context"
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/jammutkarsh/wandersort/internal/cli"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func main() {
	cfg, err := config.Defaults()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	cli.ResolveConfig(cfg)

	log := logger.New(cfg.LogLevel, cfg.LogConsole, cfg.LogFile)

	app := &cli.App{Config: cfg, Log: log}
	app.Bootstrap = func(ctx context.Context) error {
		if app.ExiftoolPath != "" {
			return nil
		}

		exiftoolPath, err := exiftool.Check(log, cfg.ExecutablePath)
		if err != nil {
			return fmt.Errorf("exiftool not found — run 'wandersort setup' first: %w", err)
		}
		app.ExiftoolPath = exiftoolPath

		if _, err := os.Stat(cfg.LocationDBPath); err != nil {
			return fmt.Errorf("location database not found — run 'wandersort setup' first")
		}

		appDB, err := db.New(ctx, cfg.AppDBPath, db.AppDB, log)
		if err != nil {
			return fmt.Errorf("app db: %w", err)
		}
		app.AppDB = appDB

		locationDB, err := db.New(ctx, cfg.LocationDBPath, db.LocationDB, log)
		if err != nil {
			appDB.Close()
			return fmt.Errorf("location db: %w", err)
		}
		app.LocationDB = locationDB

		resolver, err := location.New(locationDB, cfg.LocationDBPath, log)
		if err != nil {
			appDB.Close()
			locationDB.Close()
			return fmt.Errorf("location resolver: %w", err)
		}
		app.LocationResolver = resolver

		return nil
	}

	app.Close = func() {
		log.Info("Closing databases")
		if app.AppDB != nil {
			app.AppDB.Close()
		}
		if app.LocationDB != nil {
			app.LocationDB.Close()
		}
	}

	if err := app.Execute(); err != nil {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
		fmt.Fprintln(os.Stderr, errStyle.Render("Error:"), err)
		os.Exit(1)
	}
}
