package cli

import (
	"context"
	"fmt"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
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
			return a.runScan(v.GetStringSlice(flagPaths))
		},
	}

	cmd.Flags().StringSliceP(flagPaths, "p", nil, "Directories to scan (repeatable, or comma-separated)")
	cmd.Flags().IntP(flagWorkers, "w", 0, "Concurrent worker count")
	cmd.MarkFlagRequired(flagPaths)
	return cmd
}

func (a *App) runScan(paths []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := a.InitAppDB(ctx); err != nil {
		return err
	}
	if err := a.InitLocationResolver(ctx); err != nil {
		return err
	}
	if err := a.InitExiftool(); err != nil {
		return err
	}
	defer a.Close()

	lock, err := AcquireLock(filepath.Dir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer lock.Unlock()

	wf := workflow.NewWorkflow(ctx, a.AppDB, a.LocationResolver, a.Log, a.Config, a.ExiftoolPath)
	defer wf.Close()

	sessionID, scanPaths, err := a.PipelineService(wf).RunScan(paths)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	a.Log.Info("Scan complete", "sessionId", sessionID, "scanPaths", scanPaths)
	return nil
}
