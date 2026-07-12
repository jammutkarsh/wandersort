package cli

import (
	"context"
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/location"
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

	if _, err := exiftool.Setup(ctx, a.Log, a.Config.ExecutablePath); err != nil {
		return fmt.Errorf("exiftool: %w", err)
	}
	a.Log.Info("exiftool ready")

	if err := location.Setup(ctx, a.Log, a.Config.LocationDBPath); err != nil {
		return fmt.Errorf("location db: %w", err)
	}
	a.Log.Info("location database ready")

	return nil
}
