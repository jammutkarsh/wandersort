package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/human"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
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

// depsFailure says which dependency could not be installed and what to do.
func depsFailure(err error) error {
	failed := install.Failed(err)
	if len(failed) == 0 {
		return err
	}
	names := make([]string, len(failed))
	for i, de := range failed {
		names[i] = depLabels[de.Phase] + " (" + de.Reason() + ")"
	}
	return &shortError{
		msg: fmt.Sprintf("couldn't download %s after %d tries — check your connection and run wandersort again",
			strings.Join(names, " and "), install.MaxTries),
		err: err,
	}
}

// shortError is the on-screen message; the full cause stays for the log and errors.Is.
type shortError struct {
	err error
	msg string
}

func (e *shortError) Error() string { return e.msg }
func (e *shortError) Unwrap() error { return e.err }

func (a *app) newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add photos and videos to your library's plan",
		Long: `Reads the given folders, fingerprints every photo and video in them, works
out which are duplicates of each other, and plans where each one belongs.

Nothing is copied — 'wandersort copy' does that. This only adds
files to the plan.

Opens the app on the Add tab, the same app a bare 'wandersort' opens, so
shift+tab still reaches the settings and the plan. With --paths (-p) the run
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
	cmd.Flags().Bool(flagJSON, false, "Print one JSON result on stdout at the end (implies --plain)")
	// --paths isn't required: the Add tab asks for it when missing
	return cmd
}

// runAdd opens the shell on the Add tab; paths given on the command line skip
// the folder question.
func (a *app) runAdd(cmd *cobra.Command, paths []string, force bool) error {
	// refuse a bad folder before a library is created or a download waited on
	if _, err := path.ReduceRoots(path.New(), paths); err != nil {
		return withCode(exitUsage, err)
	}
	if a.isTuiEnabled(cmd) {
		return a.runShell(shellStart{tab: tabScan, paths: paths, force: force})
	}
	if len(paths) == 0 {
		// No screen to ask on, so this is the one place the flag is required.
		return withCode(exitUsage, fmt.Errorf("--paths (-p) is required without a terminal to ask on"))
	}
	return a.runAddPlain(paths, force)
}

// addResult is add's --json result.
type addResult struct {
	jsonOutcome
	Found      int `json:"found"`
	Read       int `json:"read"`
	Planned    int `json:"planned"`
	Unreadable int `json:"unreadable"`
	// Unopened is how many folders and files the walk could not look into.
	Unopened int `json:"unopened"`
}

// runAddPlain runs the pipeline without the TUI (--plain, --json, no terminal), one line per stage.
func (a *app) runAddPlain(paths []string, force bool) error {
	start := time.Now()
	var res addResult
	return a.emitJSON(&res, start, a.addPlain(paths, force, &res))
}

func (a *app) addPlain(paths []string, force bool, res *addResult) error {
	ctx, cancel := interruptible()
	defer cancel()

	isNew := !a.LibraryExists()
	// refused before a library is made in a folder that is to be scanned
	if _, err := workflow.CheckRoots(path.New(), a.OutputDir(), paths); err != nil {
		return err
	}
	if err := a.Open(ctx); err != nil {
		return err
	}
	defer a.closeDBs()
	library := path.New().RelativeToHome(a.OutputDir())
	if isNew {
		library += " (new, default settings)"
	}
	a.Log().Info(fmt.Sprintf("wandersort add · library %s · layout %s", library, layoutName(vfs.ConfigFor(a.Configuration()).Rules, "/")),
		logger.UserKey, true)

	a.Deps = a.newDeps(nil, nil)
	a.Deps.Start(ctx)
	if err := waitForDeps(ctx, a.Deps); err != nil {
		return err
	}

	scan, err := a.Scan(ctx, paths, force, a.workflowDeps())
	res.Found, res.Read, res.Planned = scan.Found, scan.Read, scan.Planned
	res.Unopened = len(scan.Unseen)
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("stopped; run add again to pick up where it left off")
	}
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	left, err := execute.LeftBehind(ctx, a.DB())
	if err != nil {
		return err
	}
	res.Unreadable = len(left)
	pending, _, err := execute.Pending(ctx, a.DB())
	if err != nil {
		return err
	}
	if pending == 0 {
		a.Log().Info(vfs.ErrNothingNew.Error(), logger.UserKey, true)
	} else {
		a.Log().Info("next: wandersort organise to correct the plan, or wandersort copy to copy it in", logger.UserKey, true)
	}
	if len(left) > 0 {
		return withCode(exitPartial, fmt.Errorf("%s not be read; the next add tries again", human.Plural(len(left), "file could", "files could")))
	}
	if len(scan.Unseen) > 0 {
		return withCode(exitPartial, fmt.Errorf("%s not be opened; nothing in there was added", human.Plural(len(scan.Unseen), "folder or file could", "folders and files could")))
	}
	return nil
}
