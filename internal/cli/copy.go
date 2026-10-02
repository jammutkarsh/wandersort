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
	"github.com/jammutkarsh/wandersort/pkg/human"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/report"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newCopyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "copy",
		Short: "Copy approved files into the output folder",
		Long: `Applies your 'wandersort organise' edits to the plan and copies every file
from its source into <output>/<planned folder>. Sources are never modified or
deleted; each copy is verified against the scanned hash before it counts.
Stops before changing anything if the output has too little free space. Safe
to re-run: a run interrupted partway picks up where it left off, and files that
failed before are tried again.`,
		Example: `# Copy every approved file (safe, default)
wandersort copy

# See what would happen without touching anything
wandersort copy --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runCopy(cmd)
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

func (a *app) runCopy(cmd *cobra.Command) error {
	dryRun, _ := cmd.Flags().GetBool(flagDryRun)
	if a.isTuiEnabled(cmd) && !dryRun {
		return a.runShell(shellStart{tab: tabCopy})
	}
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

	a.Log.Info("wandersort copy · library "+wspath.New().RelativeToHome(outputDir), logger.UserKey, true)
	left, err := execute.LeftBehind(ctx, a.AppDB)
	if err != nil {
		return err
	}
	res.NotRead = len(left)
	defer a.reportLeftBehind(left)

	rep, err := execute.Run(ctx, a.AppDB, a.Log, outputDir, execute.Options{
		DryRun:    dryRun,
		OnApplied: a.cleanPreviews,
	})
	if errors.Is(err, context.Canceled) {
		// every file not yet reached is still pending; nothing is half-placed
		return fmt.Errorf("stopped after %d files — run 'wandersort copy' again to carry on from there", rep.Done)
	}
	res.Copied, res.Failed, res.Bytes = rep.Done, rep.Failed, rep.Bytes
	if err != nil {
		return err
	}
	if !dryRun && (rep.Failed > 0 || len(left) > 0) {
		res.Report = a.saveFailurePage(ctx)
	}
	switch {
	case rep.Failed > 0:
		return withCode(exitPartial, fmt.Errorf("%d of %d files were not copied — the report lists each one and why", rep.Failed, rep.Done+rep.Failed))
	case len(left) > 0:
		return withCode(exitPartial, fmt.Errorf("%s not be read, so not in the library", human.Plural(len(left), "source file could", "source files could")))
	case rep.Done == 0:
		fmt.Fprintln(os.Stderr, "Nothing left to copy — run 'wandersort add' to plan more files.")
	}
	return nil
}

// newCopyScreen opens the library and builds the Copy tab over what is
// waiting to be copied.
func (a *app) newCopyScreen(session context.Context) (tui.Tab, error) {
	if err := a.openLibrary(session); err != nil {
		return nil, err
	}
	outputDir := a.Config.OutputDir()
	files, bytes, err := execute.Pending(session, a.AppDB)
	if err != nil {
		return nil, err
	}
	space, err := execute.SpaceFor(session, a.AppDB, outputDir)
	if err != nil {
		return nil, err
	}
	plan := tui.CopyPlan{
		Files: files, Bytes: bytes, Edits: a.readState(session).Edits,
		Free: int64(space.Free), FreeKnown: space.Known, Fits: space.Fits(),
	}

	// the copy's own context: its ctrl+c must not end the session
	ctx, cancel := context.WithCancel(session)
	return tui.NewCopyModel(tui.CopyConfig{
		Library: outputDir,
		Plan:    plan,
		Cancel:  cancel,
		Run: func(onStep func(string), onProgress func(string, int64, int, int)) (tui.CopyResult, error) {
			if !a.work.start() {
				return tui.CopyResult{}, context.Canceled
			}
			defer a.work.done()
			rep, err := execute.Run(ctx, a.AppDB, a.Log, outputDir, execute.Options{
				OnApplied:  a.cleanPreviews,
				OnStep:     onStep,
				OnProgress: onProgress,
			})
			res := tui.CopyResult{Copied: rep.Done, Failed: rep.Failed, Bytes: rep.Bytes}
			if err != nil {
				return res, err
			}
			left, lerr := execute.LeftBehind(ctx, a.AppDB)
			if lerr != nil {
				return res, lerr
			}
			res.NotRead = len(left)
			if res.Failed+res.NotRead > 0 {
				res.Report = a.saveFailurePage(ctx)
				res.Problems = a.copyProblems(ctx)
			}
			return res, nil
		},
	}), nil
}

// copyProblems groups the files a copy left out by reason, for the screen.
func (a *app) copyProblems(ctx context.Context) []tui.CopyProblem {
	failures, err := report.Failures(ctx, a.AppDB.SQL)
	if err != nil {
		a.Log.Warn("could not list the files left out", "error", err)
		return nil
	}
	var out []tui.CopyProblem
	index := map[string]int{}
	for _, d := range failures.Drives {
		for _, g := range d.Groups {
			i, ok := index[g.Reason]
			if !ok {
				i = len(out)
				index[g.Reason] = i
				out = append(out, tui.CopyProblem{Reason: g.Reason, Next: g.Next})
			}
			for _, f := range g.Files {
				out[i].Paths = append(out[i].Paths, wspath.New().RelativeToHome(f.Source))
			}
		}
	}
	return out
}

// cleanPreviews removes the review's peek copies once the edits are applied.
func (a *app) cleanPreviews() {
	if err := review.CleanPreviews(); err != nil {
		a.Log.Warn("could not remove the preview copies", "error", err)
	}
}

// saveFailurePage writes the failure page beside this run's log and returns its path; "" if it couldn't.
func (a *app) saveFailurePage(ctx context.Context) string {
	page := a.logFile.Page()
	if page == "" {
		return ""
	}
	failures, err := report.Failures(ctx, a.AppDB.SQL)
	if err == nil {
		err = report.SaveHTML(page, failures, report.PageInfo{
			Library: a.Config.OutputDir(), When: time.Now(), LogPath: a.logFile.Path(),
		})
	}
	if err != nil {
		a.Log.Warn("could not write the failure report", "error", err)
		return ""
	}
	a.Log.Info("Report: "+page, logger.UserKey, true)
	return page
}

// maxLeftBehindShown bounds the never-read files copy names on screen; the log has every one.
const maxLeftBehindShown = 10

// reportLeftBehind names the never-read source files no copy brings in; printed last, whatever the outcome.
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
