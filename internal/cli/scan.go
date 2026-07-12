package cli

import (
	"context"
	"fmt"
	"os/signal"
	"path/filepath"
	"strings"
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

	sessionID, scanPaths, err := a.PipelineService(wf).StartScan(paths)
	if err != nil {
		return fmt.Errorf("start scan: %w", err)
	}

	a.Log.Info("Scan started", "sessionId", sessionID, "scanPaths", scanPaths)

	wf.Close()
	return nil
}
