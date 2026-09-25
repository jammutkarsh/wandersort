// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"archive/zip"
	"bufio"
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/report"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
)

func (a *app) newAdminReportCmd() *cobra.Command {
	var includeDB, redactPaths bool
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Package logs into a zip you can attach to a bug report",
		Long: `Collects the most recent WanderSort logs into a single zip you can share when
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

// zipEntry is a source file on disk and the name it gets inside the archive.
// home, when set, is written as $HOME wherever it appears.
type zipEntry struct {
	src  string
	name string
	home string
}

// issueLogs is how many past runs' logs an issue zip carries: the run being
// reported is rarely the newest, since the user ran other commands after it.
const issueLogs = 5

func (a *app) runIssue(includeDB, redactPaths bool) error {
	home, _ := os.UserHomeDir()
	var entries []zipEntry
	for _, p := range logger.Recent(a.Config.LogDir, 0) {
		// This run's own log says nothing about the problem; an empty one is a
		// run that died before logging anything.
		if info, err := os.Stat(p); p == a.logFile.Path() || err != nil || info.Size() == 0 {
			continue
		}
		entries = append(entries, zipEntry{p, "logs/" + filepath.Base(p), home})
		if len(entries) == issueLogs {
			break
		}
	}
	if len(entries) == 0 {
		return fmt.Errorf("no log data found in %s — run a scan first", a.Config.LogDir)
	}
	if redactPaths {
		// A log line is free text: an error string, a folder name in a
		// message. The error report's rows are structured enough to redact;
		// these are not, so they stay home rather than half-redacted.
		entries = nil
	}
	logCount := len(entries)

	if includeDB {
		// Include the SQLite sidecars too so the copied DB opens cleanly.
		for _, suffix := range []string{"", "-wal", "-shm"} {
			src := a.Config.AppDBPath + suffix
			if _, err := os.Stat(src); err == nil {
				entries = append(entries, zipEntry{src, "wandersort.db" + suffix, ""})
			}
		}
		if len(entries) == logCount {
			a.Log.Warn("database not found; packaging logs only", "path", a.Config.AppDBPath)
		}
	}

	zipName := fmt.Sprintf("wandersort-issue-%s.zip", time.Now().Format("20060102-150405"))
	zipPath, err := filepath.Abs(zipName) // the current directory, not the library
	if err != nil {
		return fmt.Errorf("resolve zip path: %w", err)
	}
	zf, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip: %w", err)
	}
	defer zf.Close()

	zw := zip.NewWriter(zf)

	errorRows, summary := a.exportErrors(redactPaths)
	if w, err := zw.Create("about.txt"); err == nil {
		fmt.Fprintf(w, "wandersort admin report\ncreated: %s\nos: %s/%s\n",
			time.Now().Format(time.RFC3339), runtime.GOOS, runtime.GOARCH)
		if redactPaths {
			fmt.Fprintf(w, "\nlogs: left out (--redact-paths)\n")
		}
		if len(summary) > 0 {
			fmt.Fprintf(w, "\nfile errors (see errors.json):\n")
			for _, line := range summary {
				fmt.Fprintf(w, "  %s\n", line)
			}
		}
	}
	if errorRows != nil {
		if w, err := zw.Create("errors.json"); err == nil {
			err := json.MarshalWrite(w, errorRows, jsontext.WithIndent("  "), json.Deterministic(true))
			if err == nil {
				_, err = io.WriteString(w, "\n")
			}
			if err != nil {
				a.Log.Warn("could not write the error report", "error", err)
			}
		}
	}

	for _, e := range entries {
		if err := addFileToZip(zw, e.src, e.name, e.home); err != nil {
			zw.Close()
			return fmt.Errorf("add %s: %w", e.name, err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalize zip: %w", err)
	}

	fmt.Fprintln(os.Stderr, tui.OK.Render("Created "+zipPath))
	fmt.Fprintln(os.Stderr, tui.FaintTxt.Render("Attach this file to your bug report or share it with the maintainer."))
	return nil
}

// exportErrors reads the library's errors table for the report. It opens the
// database read-only, on its own: `issue` never takes the output lock or opens
// the library, since it is what someone runs when that is going wrong. A
// missing or busy database (another wandersort holds it) costs the error
// report, never the logs.
func (a *app) exportErrors(redactPaths bool) ([]report.Row, []string) {
	if !a.libraryExists() {
		return nil, nil
	}
	conn, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: a.Config.AppDBPath, RawQuery: "mode=ro"}).String())
	if err != nil {
		a.Log.Warn("could not open the database for the error report", "error", err)
		return nil, nil
	}
	defer conn.Close()

	home, _ := os.UserHomeDir()
	rows, summary, err := report.Errors(context.Background(), conn, report.Options{
		Home: home, Library: a.Config.OutputDir(), Redact: redactPaths,
	})
	if err != nil {
		a.Log.Warn("could not read the error report; packaging logs only", "error", err)
		return nil, nil
	}
	if rows == nil {
		rows = []report.Row{} // "no errors" is an answer: write [] rather than nothing
	}
	return rows, summary
}

// addFileToZip copies srcPath into the archive as entryName, with home, when
// set, written as $HOME on every line.
func addFileToZip(zw *zip.Writer, srcPath, entryName, home string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	w, err := zw.Create(entryName)
	if err != nil {
		return err
	}
	if home == "" {
		_, err = io.Copy(w, f)
		return err
	}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if _, werr := io.WriteString(w, report.ScrubHome(line, home)); werr != nil {
			return werr
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
