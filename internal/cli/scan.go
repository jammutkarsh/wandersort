package cli

import (
	"context"
	"fmt"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jammutkarsh/wandersort/internal/api/pipeline"
	"github.com/spf13/cobra"
)

func (a *App) newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan directories, hash files, and find duplicates",
		Long: `Scans the given paths for photos and videos, hashes them, and scores
duplicates so you can keep only the best copy.

Requires --paths (-p) to specify which directories to scan.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := v.GetString(flagPaths)
			return a.runScan(strings.Split(paths, ","))
		},
	}

	cmd.Flags().StringP(flagPaths, "p", "", "Directories to scan (comma-separated)")
	cmd.Flags().IntP(flagWorkers, "w", 0, "Concurrent worker count")
	cmd.MarkFlagRequired(flagPaths)
	return cmd
}

func (a *App) runScan(paths []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := a.Bootstrap(ctx); err != nil {
		return err
	}
	defer a.Close()

	lock, err := AcquireLock(outputDir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Unlock()

	return pipeline.RunScan(ctx, a.Log, a.AppDB, a.LocationResolver, a.Config, a.ExiftoolPath, paths)
}
