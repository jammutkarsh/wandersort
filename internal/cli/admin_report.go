package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/report"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newAdminReportCmd() *cobra.Command {
	var includeDB, redactPaths bool
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Package logs into a zip you can attach to a bug report",
		Long: `Collects the most recent logs into a single zip you can share when
something goes wrong — send it to the maintainer, or paste a log into any AI
assistant to diagnose the problem yourself.

Attach the zip to a new issue at:
  https://github.com/jammutkarsh/wandersort/issues/new/choose

Logs live in ~/.wandersort/logs, one per run that opened a library or hit a
warning or error — a run that only looked at the settings leaves none. The
last few are packaged. The zip is written to the current directory. --include-db takes the
database from your --output-path or the configured default.

The database is not included by default because it holds file paths and photo
metadata; add --include-db only if you are comfortable sharing that.

What went wrong with individual files is included regardless, as errors.json:
each failure's step, kind and technical detail, with your home directory
written as $HOME. Folder names below it stay, since they are what makes a
report debuggable — use --redact-paths to replace every path instead. The logs
get the same $HOME treatment; with --redact-paths they are left out, since
free-form log lines can't be promised path-free.`,
		Example: `wandersort admin report
wandersort admin report --redact-paths
wandersort admin report --include-db`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runIssue(includeDB, redactPaths)
		},
	}
	cmd.Flags().BoolVar(&redactPaths, "redact-paths", false, "Replace every path in the error report and leave the logs out")
	cmd.Flags().BoolVar(&includeDB, "include-db", false, "Also include the database (contains file paths and metadata)")
	// the database is nothing but paths: shipping it "redacted" would be a lie
	cmd.MarkFlagsMutuallyExclusive("redact-paths", "include-db")
	return cmd
}

func (a *app) runIssue(includeDB, redactPaths bool) error {
	zipName := fmt.Sprintf("wandersort-issue-%s.zip", time.Now().Format("20060102-150405"))
	zipPath, err := filepath.Abs(zipName) // the current directory, not the library
	if err != nil {
		return fmt.Errorf("resolve zip path: %w", err)
	}
	zf, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip: %w", err)
	}
	home, _ := os.UserHomeDir()
	err = report.Issue(context.Background(), zf, report.IssueOptions{
		LogDir: a.Configuration().LogDir, ThisRun: a.LogFile().Path(),
		Library: a.OutputDir(), Home: home,
		IncludeDB: includeDB, RedactPaths: redactPaths, Log: a.Log(),
	})
	if cerr := zf.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(zipPath) // a half-written zip is no report
		return err
	}
	fmt.Fprintln(os.Stderr, tui.OK.Render("Created "+zipPath))
	fmt.Fprintln(os.Stderr, tui.FaintTxt.Render("Attach this file to your bug report or share it with the maintainer."))
	return nil
}
