package report

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// IssueLogs is how many past runs' logs a report carries: the reported run is
// rarely the newest.
const IssueLogs = 5

// ErrNoLogs refuses an issue report with no log to carry.
var ErrNoLogs = errors.New("no log data found; run a scan first")

// IssueOptions says what an issue report carries.
type IssueOptions struct {
	// LogDir holds the run logs; ThisRun is the reporting run's own, left out.
	LogDir, ThisRun string
	// Library is the library folder whose errors (and database) go in; "" for none.
	Library string
	// Home is written as $HOME wherever it appears.
	Home string
	// IncludeDB adds the library's database; RedactPaths replaces every path
	// in errors.json and leaves the logs out. They exclude each other.
	IncludeDB, RedactPaths bool
	Log                    logger.Logger
}

// issueEntry is a file on disk and its name in the archive; home, when set,
// is written as $HOME on every line.
type issueEntry struct {
	src, name, home string
}

// Issue writes a zip for a bug report to w: about.txt, errors.json (each
// failure's step, kind and detail, paths scrubbed), the last few run logs and,
// with IncludeDB, the database. A database that can't be read costs the
// error report, never the logs.
func Issue(ctx context.Context, w io.Writer, o IssueOptions) error {
	var entries []issueEntry
	for _, p := range logger.Recent(o.LogDir, 0) {
		// This run's own log says nothing about the problem; an empty one is a
		// run that died before logging anything.
		if info, err := os.Stat(p); p == o.ThisRun || err != nil || info.Size() == 0 {
			continue
		}
		entries = append(entries, issueEntry{p, "logs/" + filepath.Base(p), o.Home})
		if len(entries) == IssueLogs {
			break
		}
	}
	if len(entries) == 0 {
		return fmt.Errorf("%w in %s", ErrNoLogs, o.LogDir)
	}
	if o.RedactPaths {
		// logs are free text and can't be reliably redacted, so they're left out
		entries = nil
	}
	if o.IncludeDB && o.Library != "" {
		logCount := len(entries)
		// the SQLite sidecars too, so the copied database opens cleanly
		for _, suffix := range []string{"", "-wal", "-shm"} {
			src := config.DBPath(o.Library) + suffix
			if _, err := os.Stat(src); err == nil {
				entries = append(entries, issueEntry{src, "wandersort.db" + suffix, ""})
			}
		}
		if len(entries) == logCount {
			o.Log.Warn("database not found; packaging logs only", "path", config.DBPath(o.Library))
		}
	}

	zw := zip.NewWriter(w)
	errorRows, summary := issueErrors(ctx, o)
	if aw, err := zw.Create("about.txt"); err == nil {
		fmt.Fprintf(aw, "wandersort admin report\ncreated: %s\nos: %s/%s\n",
			time.Now().Format(time.RFC3339), runtime.GOOS, runtime.GOARCH)
		if o.RedactPaths {
			fmt.Fprintf(aw, "\nlogs: left out (--redact-paths)\n")
		}
		if len(summary) > 0 {
			fmt.Fprintf(aw, "\nfile errors (see errors.json):\n")
			for _, line := range summary {
				fmt.Fprintf(aw, "  %s\n", line)
			}
		}
	}
	if errorRows != nil {
		if ew, err := zw.Create("errors.json"); err == nil {
			err := json.MarshalWrite(ew, errorRows, jsontext.WithIndent("  "), json.Deterministic(true))
			if err == nil {
				_, err = io.WriteString(ew, "\n")
			}
			if err != nil {
				o.Log.Warn("could not write the error report", "error", err)
			}
		}
	}
	for _, e := range entries {
		if err := addToZip(zw, e); err != nil {
			zw.Close()
			return fmt.Errorf("add %s: %w", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalize zip: %w", err)
	}
	return nil
}

// issueErrors reads the errors table, opening the database read-only on its
// own (no lock, no open session).
func issueErrors(ctx context.Context, o IssueOptions) ([]Row, []string) {
	if o.Library == "" {
		return nil, nil
	}
	if _, err := os.Stat(config.DBPath(o.Library)); err != nil {
		return nil, nil
	}
	conn, err := db.OpenReadOnly(config.DBPath(o.Library))
	if err != nil {
		o.Log.Warn("could not open the database for the error report", "error", err)
		return nil, nil
	}
	defer conn.Close()
	rows, summary, err := Errors(ctx, conn.SQL, Options{Home: o.Home, Library: o.Library, Redact: o.RedactPaths})
	if err != nil {
		o.Log.Warn("could not read the error report; packaging logs only", "error", err)
		return nil, nil
	}
	if rows == nil {
		rows = []Row{} // "no errors" is an answer: write [] rather than nothing
	}
	return rows, summary
}

// addToZip copies e.src into the archive as e.name, scrubbing e.home.
func addToZip(zw *zip.Writer, e issueEntry) error {
	f, err := os.Open(e.src)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := zw.Create(e.name)
	if err != nil {
		return err
	}
	if e.home == "" {
		_, err = io.Copy(w, f)
		return err
	}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if _, werr := io.WriteString(w, ScrubHome(line, e.home)); werr != nil {
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
