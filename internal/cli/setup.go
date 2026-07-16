package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
)

func (a *App) newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Download required dependencies (exiftool, location database)",
		Long: `Downloads and installs exiftool and the location database into
~/.wandersort. This is optional — scan and serve install anything missing on
first use — but running it up front avoids the download happening mid-scan. Safe to re-run.`,
		Example: "wandersort setup",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSetup()
		},
	}
}

func (a *App) runSetup() error {
	ctx := context.Background()

	// A running scan/serve installs dependencies itself and takes precedence, so
	// if anything is already installing, step aside instead of installing twice.
	l, err := lock.AcquireInstall(ctx, a.installDir(), false)
	if errors.Is(err, lock.ErrHeld) {
		a.Log.Info("Dependencies are already being installed by another process; nothing to do", logger.UserKey, true)
		return nil
	}
	if err != nil {
		return fmt.Errorf("install lock: %w", err)
	}
	defer l.Unlock()

	if _, err := exiftool.Setup(ctx, a.Log, a.Config.ExecutablePath); err != nil {
		return fmt.Errorf("exiftool: %w", err)
	}
	if err := location.Setup(ctx, a.Log, a.Config.LocationDBPath); err != nil {
		return fmt.Errorf("location db: %w", err)
	}

	a.Log.Info("Setup complete. You're ready to scan.", logger.UserKey, true)
	return nil
}
