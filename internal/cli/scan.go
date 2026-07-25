// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
)

func (a *App) newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan directories, hash files, and find duplicates",
		Long: `Scans the given paths for photos and videos, hashes them, and scores
duplicates so you can keep only the best copy.

Requires --paths (-p) to specify which directories to scan. The scan runs in
the foreground and reports progress until it finishes.

Run 'wandersort config' once before the first scan — the proposed folder
structure is built from those settings.`,
		Example: `# Scan a single directory
wandersort scan --paths ~/Pictures

# Scan multiple directories (repeat -p or comma-separate)
wandersort scan -p ~/Pictures -p /Volumes/SD
wandersort scan -p ~/Pictures,/Volumes/SD

# Use 8 workers and a custom output directory
wandersort scan -p ~/Pictures -w 8 -o ~/wandersort-out`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireConfigured(); err != nil {
				return err
			}
			return a.runScan(v.GetStringSlice(flagPaths))
		},
	}

	cmd.Flags().StringSliceP(flagPaths, "p", nil, "Directories to scan (repeatable, or comma-separated)")
	cmd.Flags().IntP(flagWorkers, "w", 0, "Concurrent worker count")
	cmd.MarkFlagRequired(flagPaths)
	return cmd
}

func (a *App) runScan(paths []string) error {
	if a.tuiEnabled() {
		return a.runScanTUI(paths)
	}
	return a.runScanPlain(paths)
}

// runScanPlain is the non-TUI path: synchronous pipeline, progress via the
// console logger's line output. Used with --plain or a non-terminal
// stderr. Behaviour is unchanged from before the TUI existed.
func (a *App) runScanPlain(paths []string) error {
	start := time.Now()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := a.InitAppDB(ctx); err != nil {
		return err
	}
	if err := a.EnsureDependencies(ctx); err != nil {
		return err
	}
	defer a.Close()

	if err := a.syncHomeWorkFromConfig(ctx); err != nil {
		return fmt.Errorf("anchors: %w", err)
	}

	l, err := lock.AcquireOutput(filepath.Dir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer l.Unlock()

	wf := workflow.NewWorkflow(ctx, a.AppDB, a.Log, a.Config, workflow.ReadyDeps(a.ExiftoolPath, a.LocationResolver))
	defer wf.Close()

	sessionID, scanPaths, err := a.PipelineService(wf).RunScan(paths)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	hint := "wandersort review"
	if outputPath := v.GetString(flagOutputPath); outputPath != "" {
		hint = fmt.Sprintf("wandersort review -o %s", outputPath)
	}
	a.Log.Info(fmt.Sprintf("Scan complete in %s. Run '%s' to review the proposed folders.", time.Since(start).Round(time.Millisecond), hint),
		logger.UserKey, true, "sessionId", sessionID, "scanPaths", scanPaths)
	return nil
}

// runScanTUI is the full-screen path. The scan starts immediately — there is
// no install screen. Missing dependencies download in the background (byte
// progress renders as a row on the scan screen) and each pipeline phase waits
// only for its own dependency (workflow.Deps): exif on exiftool, vfs on the
// location database, scan/hash on nothing. On a first-ever run the file walk
// and hashing overlap the downloads instead of sitting behind them.
// The pipeline logs through a TUI logger whose Sink forwards every user/stream
// Event into the program; on a clean scan the user can drop straight into review.
func (a *App) runScanTUI(paths []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := a.InitAppDB(ctx); err != nil {
		return err
	}
	l, err := lock.AcquireOutput(filepath.Dir(a.Config.LogFile))
	if err != nil {
		a.Close()
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer a.Close()
	defer l.Unlock()

	// TUI logger: user/stream Events flow to the program; the JSON file log still
	// captures everything. The forwarding goroutine outlives Run() and is left to
	// exit with the process — the send never deadlocks because it always drains.
	// a.Log is swapped so dependency-install milestones land in the TUI too.
	events := make(chan logger.Event, 4096)
	tuiLog := logger.NewTUI(a.Config.LogLevel, a.Config.LogFile, func(e logger.Event) { events <- e })
	origLog := a.Log
	a.Log = tuiLog
	defer func() { a.Log = origLog }()

	// Install in the background with a checkpoint after exiftool (the small
	// download): exifReady gates the exif phase, allReady gates the vfs phase.
	// Closed channels are the happens-before edge making a.ExiftoolPath and
	// a.LocationResolver safe to read from the pipeline goroutine.
	var exifErr, depsErr error
	exifReady := make(chan struct{})
	allReady := make(chan struct{})
	go func() {
		depsErr = a.ensureDependencies(ctx, func(err error) {
			exifErr = err
			close(exifReady)
		})
		close(allReady)
	}()

	// await logs why a phase is stalled, but only if it actually stalls —
	// installed dependencies leave no trace.
	await := func(ch <-chan struct{}, why string) {
		select {
		case <-ch:
		default:
			tuiLog.Info(why, logger.UserKey, true)
			<-ch
		}
	}
	deps := workflow.Deps{
		Exiftool: func() (string, error) {
			await(exifReady, "Waiting for the exiftool download to finish…")
			return a.ExiftoolPath, exifErr
		},
		Location: func() (*location.Resolver, error) {
			await(allReady, "Waiting for the location database download to finish…")
			if depsErr != nil {
				return nil, depsErr
			}
			// anchors need the resolver, so this is the earliest they can sync;
			// vfs is also the only phase that reads them
			if err := a.syncHomeWorkFromConfig(ctx); err != nil {
				return nil, fmt.Errorf("anchors: %w", err)
			}
			return a.LocationResolver, nil
		},
	}

	wf := workflow.NewWorkflow(ctx, a.AppDB, tuiLog, a.Config, deps)
	defer wf.Close()
	first := tui.NewScanModel(tui.ScanConfig{
		Pipeline: func() error {
			_, _, err := a.PipelineService(wf).RunScan(paths)
			return err
		},
		Cancel: cancel,
		// Swap straight into review in the same program, reusing the open DB
		// and held lock. The scan screen calls this on the post-scan prompt.
		ReviewNext: func() (tea.Model, error) {
			return a.newReviewScreen(ctx)
		},
	})

	prog := tea.NewProgram(tui.NewShell(first), tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	a.InstallProgress = func(phase string, done, total int64) {
		prog.Send(tui.InstallProgressMsg{Phase: phase, Done: done, Total: total})
	}
	go func() {
		for e := range events {
			prog.Send(tui.LogEventMsg{Event: e})
		}
	}()

	final, runErr := prog.Run()

	a.InstallProgress = nil
	if runErr != nil {
		return runErr
	}

	if shell, ok := final.(tui.Shell); ok {
		if cur, ok := shell.Current().(reviewScreen); ok {
			return a.reportReviewOutcome(cur)
		}
	}
	return nil
}

// reportReviewOutcome prints the in-program review result after the alt-screen
// has been torn down (so the message lands on the plain terminal).
func (a *App) reportReviewOutcome(rs reviewScreen) error {
	switch {
	case rs.finalErr != nil:
		return fmt.Errorf("save plan: %w", rs.finalErr)
	case !rs.confirmed:
		a.Log.Info("Review cancelled — nothing changed", logger.UserKey, true)
	default:
		a.Log.Info("Folder structure approved. Confirmed names will be suggested on future scans.", logger.UserKey, true)
	}
	return nil
}
