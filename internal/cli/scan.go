package cli

import (
	"context"
	"fmt"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
)

func (a *App) newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan directories, hash files, and find duplicates",
		Long: `Scans the given paths for photos and videos, hashes them, and scores
duplicates so you can keep only the best copy.

Requires --paths (-p) to specify which directories to scan. The scan runs in
the foreground and reports progress until it finishes.`,
		Example: `# Scan a single directory
wandersort scan --paths ~/Pictures

# Scan multiple directories (repeat -p or comma-separate)
wandersort scan -p ~/Pictures -p /Volumes/SD
wandersort scan -p ~/Pictures,/Volumes/SD

# Use 8 workers and a custom output directory
wandersort scan -p ~/Pictures -w 8 -o ~/wandersort-out`,
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
	if err := a.EnsureDependencies(ctx); err != nil {
		return err
	}
	defer a.Close()

	l, err := lock.AcquireOutput(filepath.Dir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer l.Unlock()

	wf := workflow.NewWorkflow(ctx, a.AppDB, a.LocationResolver, a.Log, a.Config, a.ExiftoolPath)
	defer wf.Close()

	sessionID, scanPaths, err := a.PipelineService(wf).RunScan(paths)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	hint := "wandersort report"
	if outputPath := v.GetString(flagOutputPath); outputPath != "" {
		hint = fmt.Sprintf("wandersort report -o %s", outputPath)
	}
	a.Log.Info(fmt.Sprintf("Scan complete. Run '%s' to see the results.", hint), logger.UserKey, true, "sessionId", sessionID, "scanPaths", scanPaths)
	return nil
}
