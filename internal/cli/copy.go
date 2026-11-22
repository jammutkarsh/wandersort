package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/library"
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
	Report  string `json:"report,omitempty"`
	Copied  int    `json:"copied"`
	Failed  int    `json:"failed"`
	NotRead int    `json:"notRead"`
	Bytes   int64  `json:"bytes"`
	// Targets is where a dry run would place each file, library-relative.
	Targets []string `json:"targets,omitempty"`
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
	if !a.LibraryExists() {
		return a.noLibrary()
	}

	outputDir := a.OutputDir()

	ctx, cancel := interruptible()
	defer cancel()
	if err := a.Open(ctx); err != nil {
		return err
	}
	defer a.closeDBs()

	a.Log().Info("wandersort copy · library "+wspath.New().RelativeToHome(outputDir), logger.UserKey, true)

	opts := execute.Options{DryRun: dryRun}
	if dryRun {
		opts.OnProgress = func(target string, _ int64, _, _ int) { res.Targets = append(res.Targets, target) }
	}
	done, err := a.Copy(ctx, opts)
	rep, left := done.Report, done.NotRead
	res.Copied, res.Failed, res.Bytes, res.NotRead, res.Report = rep.Done, rep.Failed, rep.Bytes, len(left), done.Page
	defer a.reportLeftBehind(left)
	if !a.jsonOut {
		for _, t := range res.Targets {
			fmt.Println(t)
		}
	}
	if errors.Is(err, context.Canceled) {
		// every file not yet reached is still pending; nothing is half-placed
		return fmt.Errorf("stopped after %d files; run copy again to resume", rep.Done)
	}
	if err != nil {
		return err
	}
	switch {
	case rep.Failed > 0:
		return withCode(exitPartial, fmt.Errorf("%d of %d files not copied; see the report", rep.Failed, rep.Done+rep.Failed))
	case len(left) > 0:
		return withCode(exitPartial, fmt.Errorf("%s not be read, so not in the library", human.Plural(len(left), "source file could", "source files could")))
	case rep.Done == 0:
		fmt.Fprintln(os.Stderr, "Nothing left to copy — run 'wandersort add' to plan more files.")
	}
	return nil
}

// newCopyScreen opens the library and builds the Copy tab over what waits to be copied; stop ends its context.
func (a *app) newCopyScreen(session context.Context) (screen tui.Tab, stop context.CancelFunc, err error) {
	if err := a.Open(session); err != nil {
		return nil, nil, err
	}
	outputDir := a.OutputDir()
	p, err := a.CopyPlan(session)
	if err != nil {
		return nil, nil, err
	}
	plan := tui.CopyPlan{
		Files: p.Files, Bytes: p.Bytes, Edits: p.Edits,
		Free: int64(p.Space.Free), FreeKnown: p.Space.Known, Fits: p.Space.Fits(),
	}

	// the copy's own context: its ctrl+c must not end the session
	ctx, cancel := context.WithCancel(session)
	screen = tui.NewCopyModel(tui.CopyConfig{
		Library: outputDir,
		Plan:    plan,
		Cancel:  cancel,
		Run: func(onStep func(string), onProgress func(string, int64, int, int)) (tui.CopyResult, error) {
			done, err := a.Copy(ctx, execute.Options{OnStep: onStep, OnProgress: onProgress})
			if errors.Is(err, library.ErrClosing) {
				err = context.Canceled // quitting: the screen is going too
			}
			return tui.CopyResult{
				Copied: done.Done, Failed: done.Failed, Bytes: done.Bytes, NotRead: len(done.NotRead),
				Report: done.Page, Problems: copyProblems(done.Failures),
			}, err
		},
	})
	return screen, cancel, nil
}

// copyProblems is the files a copy left out, by reason, for the screen.
func copyProblems(failures report.FailureReport) []tui.CopyProblem {
	var out []tui.CopyProblem
	for _, g := range failures.ByReason() {
		p := tui.CopyProblem{Reason: g.Reason, Next: g.Next}
		for _, f := range g.Files {
			p.Paths = append(p.Paths, wspath.New().RelativeToHome(f.Source))
		}
		out = append(out, p)
	}
	return out
}

// maxLeftBehindShown bounds the never-read files copy names on screen; the log has every one.
const maxLeftBehindShown = 10

// reportLeftBehind names the never-read source files no copy brings in; printed last, whatever the outcome.
func (a *app) reportLeftBehind(left []string) {
	if len(left) == 0 {
		return
	}
	for _, p := range left {
		a.Log().Info("never read, so not transferred", "source", p)
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
