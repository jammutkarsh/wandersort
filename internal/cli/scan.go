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
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
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
	cmd.Flags().StringSlice(flagRules, nil,
		`Folder levels below Year/Month the proposal will use, i.e. group by (repeatable or comma-separated): location, date, device, orientation, media, or "none" for flat Year/Month. Can also be changed later, per-session, from 'wandersort review' ([L] key)`)
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

	wf := workflow.NewWorkflow(ctx, a.AppDB, a.LocationResolver, a.Log, a.Config, a.ExiftoolPath)
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

// runScanTUI is the full-screen path. Everything the user waits on renders in
// one alt-screen program: missing dependencies download on the install screen
// (byte progress bars) which then swaps into the scan screen, so a first-ever
// `wandersort scan` never drops to plain console output. The pipeline logs through a TUI logger whose Sink forwards every
// user/stream Event into the program; on a clean scan the user can drop
// straight into review.
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

	// buildScan runs once dependencies exist (EnsureDependencies opened the
	// resolver + exiftool): sync anchors, build the workflow, hand back the
	// scan screen.
	var wf *workflow.Workflow
	buildScan := func() (tea.Model, error) {
		if err := a.syncHomeWorkFromConfig(ctx); err != nil {
			return nil, fmt.Errorf("anchors: %w", err)
		}
		wf = workflow.NewWorkflow(ctx, a.AppDB, a.LocationResolver, tuiLog, a.Config, a.ExiftoolPath)
		return tui.NewScanModel(tui.ScanConfig{
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
		}), nil
	}

	// Deps present → skip the install screen (it would just flash "done").
	var first tea.Model
	if exiftool.Installed(a.Log, a.Config.ExecutablePath) && location.Installed(a.Config.LocationDBPath) {
		if err := a.EnsureDependencies(ctx); err != nil {
			a.Log = origLog
			return err
		}
		if first, err = buildScan(); err != nil {
			a.Log = origLog
			return err
		}
	} else {
		im := tui.NewInstallModel(func() error { return a.EnsureDependencies(ctx) })
		im.Next = buildScan
		first = im
	}

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

	a.Log = origLog
	a.InstallProgress = nil
	if wf != nil {
		wf.Close()
	}
	if runErr != nil {
		return runErr
	}

	if shell, ok := final.(tui.Shell); ok {
		switch cur := shell.Current().(type) {
		case tui.InstallModel:
			// Never reached the scan screen: install failed or was cancelled.
			return cur.Err()
		case reviewScreen:
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
