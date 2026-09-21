// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// waitForDeps blocks until both downloadable dependencies are ready, so no
// pipeline phase starts until they are: dependencies used to download in the
// background while the walk ran, and a failed download surfaced through
// whichever phase happened to be running when it gave up — "pipeline
// cancelled during metadata phase" for an ordinary network failure, not a
// cancellation. Downloading first trades that overlap for a single, clear
// failure before any file is touched.
func waitForDeps(deps *install.Coordinator) error {
	for _, d := range []struct {
		name string
		get  func() error
	}{
		{"exiftool", func() error { _, err := deps.Exiftool(); return err }},
		{"location database", func() error { _, err := deps.Location(); return err }},
	} {
		// The full technical error (URL, transport failure) already went to
		// the log file via the retry warnings — the user just needs to know
		// what to do next.
		if err := d.get(); err != nil {
			return fmt.Errorf("failed to download the %s — retry the scan to download it again", d.name)
		}
	}
	return nil
}

func (a *app) newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add photos and videos to your library's plan",
		Long: `Reads the given folders, fingerprints every photo and video in them, works
out which are duplicates of each other, and plans where each one belongs.

Nothing is copied or moved — 'wandersort organise' does that. This only adds
files to the plan.

Opens WanderSort on the Add tab, the same app a bare 'wandersort' opens, so
ctrl+t still reaches the settings and the plan. With --paths (-p) the run
starts straight away; without it you are asked which folders to add.

--paths is required with --plain (or a non-terminal stderr): there is no
screen to ask on.`,
		Example: `# Pick the folders on screen
wandersort add

# Add a single directory
wandersort add --paths ~/Pictures

# Add several (repeat -p or comma-separate)
wandersort add -p ~/Pictures -p /Volumes/SD
wandersort add -p ~/Pictures,/Volumes/SD

# Add into a particular library
wandersort add -p ~/Pictures -o ~/wandersort-out`,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, _ := cmd.Flags().GetStringSlice(flagPaths)
			force, _ := cmd.Flags().GetBool(flagForce)
			return a.runAdd(cmd, paths, force)
		},
	}

	cmd.Flags().StringSliceP(flagPaths, "p", nil,
		"Directories to add (repeatable, or comma-separated). Asked for on screen if omitted")
	cmd.Flags().Bool(flagForce, false,
		"Re-read every already-scanned file from disk instead of skipping unchanged ones")
	// Deliberately not MarkFlagRequired: the Add tab's own folder input is the
	// answer when it's missing, and refusing to open the app over a question it
	// is about to ask makes `add` the one command that can't just be run.
	return cmd
}

// runAdd opens the app on the Add tab — the same session a bare `wandersort`
// gives, so ctrl+t still reaches the settings and the plan. Paths given on
// the command line skip the folder question; without them the tab opens on it.
func (a *app) runAdd(cmd *cobra.Command, paths []string, force bool) error {
	if a.isTuiEnabled(cmd) {
		return a.runShell(shellStart{tab: tabScan, paths: paths, force: force})
	}
	if len(paths) == 0 {
		// No screen to ask on, so this is the one place the flag is required.
		return fmt.Errorf("--paths (-p) is required without a terminal to ask on")
	}
	return a.runAddPlain(paths, force)
}

// runAddPlain is the non-TUI path: synchronous pipeline, progress via the
// console logger's line output. Used with --plain or a non-terminal
// stderr. Behaviour is unchanged from before the TUI existed. force is
// --force's explicit consent to re-read every file already added — no
// confirmation prompt needed.
func (a *app) runAddPlain(paths []string, force bool) error {
	start := time.Now()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := a.openLibrary(ctx); err != nil {
		return err
	}
	a.Deps = a.newDeps(nil)
	a.Deps.Start(ctx)
	defer a.closeDBs()

	if err := waitForDeps(a.Deps); err != nil {
		return err
	}

	wf := workflow.NewWorkflow(ctx, a.AppDB, a.Log, a.Config, a.workflowDeps())

	scanPaths, err := wf.RunScan(paths, force)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	// No -o needed: the library just added to is the one the next launch
	// opens on (config.New reads the history this run wrote).
	a.Log.Info(fmt.Sprintf("Added in %s. Run 'wandersort organise' to review the plan and move the files.", time.Since(start).Round(time.Millisecond)),
		logger.UserKey, true, "addedPaths", scanPaths)
	return nil
}
