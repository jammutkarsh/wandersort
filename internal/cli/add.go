package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// waitForDeps blocks until both downloadable dependencies are ready, so a
// failed download is one clear error before any file is touched.
func waitForDeps(ctx context.Context, deps *install.Coordinator) error {
	if _, err := deps.Exiftool(ctx); err != nil {
		return depsFailure(err)
	}
	if _, err := deps.Location(ctx); err != nil {
		return depsFailure(err)
	}
	return nil
}

// depsFailure says which dependency could not be installed and what to do;
// the full error is already in the log.
func depsFailure(err error) error {
	var de *install.DependencyError
	if !errors.As(err, &de) {
		return err
	}
	return fmt.Errorf("couldn't download %s after %d tries (%s) — check your connection and run wandersort again",
		strings.ToLower(depLabels[de.Phase]), install.MaxTries, de.Reason())
}

func (a *app) newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add photos and videos to your library's plan",
		Long: `Reads the given folders, fingerprints every photo and video in them, works
out which are duplicates of each other, and plans where each one belongs.

Nothing is copied — 'wandersort execute' does that. This only adds
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
	// --paths isn't required: the Add tab asks for it when missing
	return cmd
}

// runAdd opens the shell on the Add tab; paths given on the command line skip
// the folder question.
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

// runAddPlain is the non-TUI path (--plain or non-terminal stderr): runs the
// pipeline synchronously with line output. force re-reads every file.
func (a *app) runAddPlain(paths []string, force bool) error {
	start := time.Now()
	ctx, cancel := interruptible()
	defer cancel()

	if err := a.openLibrary(ctx); err != nil {
		return err
	}
	a.Deps = a.newDeps(nil, nil)
	a.Deps.Start(ctx)
	defer a.closeDBs()

	if err := waitForDeps(ctx, a.Deps); err != nil {
		return err
	}

	wf := workflow.NewWorkflow(a.AppDB, a.Log, a.Config, a.workflowDeps())

	scanPaths, err := wf.RunScan(ctx, paths, force)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	// no -o needed: the next launch opens the library just used
	a.Log.Info(fmt.Sprintf("Added in %s. Run 'wandersort organise' to correct the plan, then 'wandersort execute' to copy the files in.", time.Since(start).Round(time.Millisecond)),
		logger.UserKey, true, "addedPaths", scanPaths)
	return nil
}
