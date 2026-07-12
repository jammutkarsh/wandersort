package cli

import (
	"context"
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/spf13/cobra"
)

func (a *App) newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Download required dependencies (exiftool, location database)",
		Long:  "Downloads and installs exiftool and the location database. Run this once before scanning.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSetup()
		},
	}
}

func (a *App) runSetup() error {
	ctx := context.Background()

	if _, err := exiftool.Verify(ctx, a.Log, a.Config.ExecutablePath); err != nil {
		return fmt.Errorf("exiftool: %w", err)
	}
	a.Log.Info("exiftool ready")

	locDB, err := db.New(ctx, a.Config.LocationDBPath, db.LocationDB, a.Log)
	if err != nil {
		return fmt.Errorf("location db: %w", err)
	}
	locDB.Close()
	a.Log.Info("location database ready")

	return nil
}
