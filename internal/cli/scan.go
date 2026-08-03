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

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
)

func (a *app) newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan directories, hash files, and find duplicates",
		Long: `Scans the given paths for photos and videos, hashes them, and scores
duplicates so you can keep only the best copy.

Requires --paths (-p) to specify which directories to scan. The scan runs in
the foreground and reports progress until it finishes.

Runs on defaults if you haven't run 'wandersort config' yet. Run it later and
'wandersort review --rebuild' to re-propose the folder structure from your
settings without re-scanning.`,
		Example: `# Scan a single directory
wandersort scan --paths ~/Pictures

# Scan multiple directories (repeat -p or comma-separate)
wandersort scan -p ~/Pictures -p /Volumes/SD
wandersort scan -p ~/Pictures,/Volumes/SD

# Use 8 workers and a custom output directory
wandersort scan -p ~/Pictures -w 8 -o ~/wandersort-out`,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, _ := cmd.Flags().GetStringSlice(flagPaths)
			return a.runScan(cmd, paths)
		},
	}

	cmd.Flags().StringSliceP(flagPaths, "p", nil, "Directories to scan (repeatable, or comma-separated)")
	cmd.Flags().IntP(flagWorkers, "w", 0, "Concurrent worker count")
	cmd.MarkFlagRequired(flagPaths)
	return cmd
}

func (a *app) runScan(cmd *cobra.Command, paths []string) error {
	if a.isTuiEnabled(cmd) {
		return a.runScanTUI(paths)
	}
	return a.runScanPlain(paths)
}

// runScanPlain is the non-TUI path: synchronous pipeline, progress via the
// console logger's line output. Used with --plain or a non-terminal
// stderr. Behaviour is unchanged from before the TUI existed.
func (a *app) runScanPlain(paths []string) error {
	start := time.Now()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	l, err := a.lockOutput()
	if err != nil {
		return err
	}
	defer l.Unlock()

	if err := a.initAppDB(ctx); err != nil {
		return err
	}
	a.Deps = a.newDeps(nil)
	a.Deps.Start(ctx)
	defer a.closeDBs()

	wf := workflow.NewWorkflow(ctx, a.AppDB, a.Log, a.Config, a.workflowDeps())

	scanPaths, err := wf.RunScan(paths)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	hint := "wandersort review"
	if a.Config.Configured {
		hint = fmt.Sprintf("wandersort review -o %s", filepath.Dir(a.Config.AppDBPath))
	}
	a.Log.Info(fmt.Sprintf("Scan complete in %s. Run '%s' to review the proposed folders.", time.Since(start).Round(time.Millisecond), hint),
		logger.UserKey, true, "scanPaths", scanPaths)
	return nil
}

// runScanTUI is the full-screen path: the scan starts immediately, with
// missing dependencies downloading in the background instead of behind an
// install screen.
func (a *app) runScanTUI(paths []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := a.initAppDB(ctx); err != nil {
		return err
	}
	l, err := a.lockOutput()
	if err != nil {
		a.closeDBs()
		return err
	}
	defer a.closeDBs()
	defer l.Unlock()

	// Events flow to the program; forwarding goroutine outlives Run() and exits
	// with the process — the send never deadlocks since the program always drains it.
	events := make(chan logger.Event, 4096)
	tuiLog := logger.NewTUI(a.Config.LogLevel, a.Config.LogFile, func(e logger.Event) { events <- e })
	origLog := a.Log
	a.Log = tuiLog // swapped so dependency-install milestones land in the TUI too
	defer func() { a.Log = origLog }()

	// each phase gates only on its own dependency (exif on exiftool, vfs on the
	// location DB), so a first-ever run's walk/hash overlap the downloads
	wf := workflow.NewWorkflow(ctx, a.AppDB, tuiLog, a.Config, a.workflowDeps())
	first := tui.NewScanModel(tui.ScanConfig{
		Pipeline: func() error {
			_, err := wf.RunScan(paths)
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
	a.Deps = a.newDeps(func(phase string, done, total int64) {
		prog.Send(tui.InstallProgressMsg{Phase: phase, Done: done, Total: total})
	})
	a.Deps.Start(ctx)
	go func() {
		for e := range events {
			prog.Send(tui.LogEventMsg{Event: e})
		}
	}()

	final, runErr := prog.Run()
	if runErr != nil {
		return runErr
	}

	if shell, ok := final.(tui.Shell); ok {
		if confirmed, saveErr, ok := review.Outcome(shell.Current()); ok {
			return a.reportReviewOutcome(confirmed, saveErr)
		}
	}
	return nil
}
