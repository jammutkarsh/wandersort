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
	"path/filepath"
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

func (a *app) newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan directories, hash files, and find duplicates",
		Long: `Scans the given paths for photos and videos, hashes them, and scores
duplicates so you can keep only the best copy.

Opens WanderSort on the scan tab — the same app a bare 'wandersort' opens, so
ctrl+t still reaches the settings and the review. With --paths (-p) the run
starts straight away; without it you are asked which folders to scan.

--paths is required with --plain (or a non-terminal stderr): there is no
screen to ask on.

Runs on defaults if you haven't run 'wandersort config' yet. Run it later and
'wandersort review --rebuild' to re-propose the folder structure from your
settings without re-scanning.`,
		Example: `# Pick the folders on screen
wandersort scan

# Scan a single directory
wandersort scan --paths ~/Pictures

# Scan multiple directories (repeat -p or comma-separate)
wandersort scan -p ~/Pictures -p /Volumes/SD
wandersort scan -p ~/Pictures,/Volumes/SD

# Scan into a custom output directory
wandersort scan -p ~/Pictures -o ~/wandersort-out`,
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, _ := cmd.Flags().GetStringSlice(flagPaths)
			force, _ := cmd.Flags().GetBool(flagForce)
			return a.runScan(cmd, paths, force)
		},
	}

	cmd.Flags().StringSliceP(flagPaths, "p", nil,
		"Directories to scan (repeatable, or comma-separated). Asked for on screen if omitted")
	cmd.Flags().Bool(flagForce, false,
		"Re-read every already-scanned file from disk instead of skipping unchanged ones")
	// Deliberately not MarkFlagRequired: the scan tab's own folder input is the
	// answer when it's missing, and refusing to open the app over a question it
	// is about to ask makes `scan` the one command that can't just be run.
	return cmd
}

// runScan opens the app on the scan tab — the same session a bare `wandersort`
// gives, so ctrl+t still reaches the settings and the review. Paths given on
// the command line skip the folder question; without them the tab opens on it.
func (a *app) runScan(cmd *cobra.Command, paths []string, force bool) error {
	if a.isTuiEnabled(cmd) {
		return a.runShell(shellStart{tab: tabScan, paths: paths, force: force})
	}
	if len(paths) == 0 {
		// No screen to ask on, so this is the one place the flag is required.
		return fmt.Errorf("--paths (-p) is required without a terminal to ask on")
	}
	return a.runScanPlain(paths, force)
}

// runScanPlain is the non-TUI path: synchronous pipeline, progress via the
// console logger's line output. Used with --plain or a non-terminal
// stderr. Behaviour is unchanged from before the TUI existed. force is
// --force's explicit consent to re-read every already-scanned file — no
// confirmation prompt, same as --rebuild.
func (a *app) runScanPlain(paths []string, force bool) error {
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

	if err := waitForDeps(a.Deps); err != nil {
		return err
	}

	wf := workflow.NewWorkflow(ctx, a.AppDB, a.Log, a.Config, a.workflowDeps())

	scanPaths, err := wf.RunScan(paths, force)
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
