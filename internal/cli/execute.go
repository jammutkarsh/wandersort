package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/internal/review"
	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newExecuteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "execute",
		Short: "Copy approved files into the output folder",
		Long: `Applies your 'wandersort organise' edits to the plan and copies every file
from its source into <output>/<planned folder>. Sources are never modified or
deleted; each copy is verified against the scanned hash before it counts.
Stops before changing anything if the output has too little free space. Safe
to re-run: a run interrupted partway picks up where it left off, and files that
failed before are tried again.`,
		Example: `# Copy every approved file (safe, default)
wandersort execute

# See what would happen without touching anything
wandersort execute --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runExecute(cmd)
		},
	}

	cmd.Flags().Bool(flagDryRun, false, "Report what would be transferred, and where, without touching anything")
	cmd.Flags().Bool(flagJSON, false, "Print one JSON result on stdout at the end (implies --plain)")
	return cmd
}

// copyResult is the copy command's --json result.
type copyResult struct {
	jsonOutcome
	Copied  int    `json:"copied"`
	Failed  int    `json:"failed"`
	NotRead int    `json:"notRead"`
	Bytes   int64  `json:"bytes"`
	Report  string `json:"report,omitempty"`
}

func (a *app) runExecute(cmd *cobra.Command) error {
	dryRun, _ := cmd.Flags().GetBool(flagDryRun)
	start := time.Now()
	var res copyResult
	return a.emitJSON(&res, start, a.copyPlain(dryRun, &res))
}

func (a *app) copyPlain(dryRun bool, res *copyResult) error {
	if !a.libraryExists() {
		return fmt.Errorf("no database found — run 'wandersort add' first")
	}

	outputDir := a.Config.OutputDir()

	ctx, cancel := interruptible()
	defer cancel()
	if err := a.openLibrary(ctx); err != nil {
		return err
	}
	defer a.closeDBs()

	left, err := execute.LeftBehind(ctx, a.AppDB)
	if err != nil {
		return err
	}
	res.NotRead = len(left)
	defer a.reportLeftBehind(left)

	rep, err := execute.Run(ctx, a.AppDB, a.Log, outputDir, execute.Options{
		DryRun: dryRun,
		OnApplied: func() {
			if err := review.CleanPreviews(); err != nil {
				a.Log.Warn("could not remove the preview copies", "error", err)
			}
		},
	})
	if errors.Is(err, context.Canceled) {
		// every file not yet reached is still pending; nothing is half-placed
		return fmt.Errorf("stopped after %d files — run 'wandersort execute' again to carry on from there", rep.Done)
	}
	res.Copied, res.Failed, res.Bytes = rep.Done, rep.Failed, rep.Bytes
	if err != nil {
		return err
	}
	switch {
	case rep.Failed > 0:
		return withCode(exitPartial, fmt.Errorf("%d of %d files were not copied — see the log for why", rep.Failed, rep.Done+rep.Failed))
	case len(left) > 0:
		return withCode(exitPartial, fmt.Errorf("%d source files could not be read, so they are not in the library", len(left)))
	case rep.Done == 0:
		fmt.Fprintln(os.Stderr, "Nothing left to copy — run 'wandersort add' to plan more files.")
	}
	return nil
}

// maxLeftBehindShown bounds how many never-read files execute names on
// screen; the log has every one.
const maxLeftBehindShown = 10

// reportLeftBehind names source files no transfer will bring in because they
// were never read. Printed last, whatever the outcome.
func (a *app) reportLeftBehind(left []string) {
	if len(left) == 0 {
		return
	}
	for _, p := range left {
		a.Log.Info("never read, so not transferred", "source", p)
	}
	paths := wspath.New()
	fmt.Fprintf(os.Stderr, "%s %d source files could not be read, so they are not in the library:\n",
		tui.Attn.Render("⚠"), len(left))
	for _, p := range left[:min(len(left), maxLeftBehindShown)] {
		fmt.Fprintf(os.Stderr, "    %s\n", paths.RelativeToHome(p))
	}
	if len(left) > maxLeftBehindShown {
		fmt.Fprintf(os.Stderr, "    … and %d more (see the log)\n", len(left)-maxLeftBehindShown)
	}
	fmt.Fprintln(os.Stderr, "  Keep these sources; 'wandersort add' tries them again.")
}
