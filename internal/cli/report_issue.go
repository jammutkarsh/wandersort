package cli

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

func (a *App) newReportIssueCmd() *cobra.Command {
	var includeDB bool
	cmd := &cobra.Command{
		Use:   "report-issue",
		Short: "Package logs into a zip you can attach to a bug report",
		Long: `Collects the WanderSort log into a single zip you can share when something
goes wrong — send it to the maintainer, or paste the log into any AI assistant
to diagnose the problem yourself.

The database is not included by default because it holds file paths and photo
metadata; add --include-db only if you are comfortable sharing that.`,
		Example: `wandersort report-issue
wandersort report-issue --include-db`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReportIssue(includeDB)
		},
	}
	cmd.Flags().BoolVar(&includeDB, "include-db", false, "Also include the database (contains file paths and metadata)")
	return cmd
}

// zipEntry is a source file on disk and the name it gets inside the archive.
type zipEntry struct {
	src  string
	name string
}

func (a *App) runReportIssue(includeDB bool) error {
	// The logger creates an empty log file at startup, so existence alone is not
	// enough — treat an empty log as "nothing to report".
	if info, err := os.Stat(a.Config.LogFile); err != nil || info.Size() == 0 {
		return fmt.Errorf("no log data found at %s — run a scan first", a.Config.LogFile)
	}
	entries := []zipEntry{{a.Config.LogFile, "wandersort.log"}}

	if includeDB {
		// Include the SQLite sidecars too so the copied DB opens cleanly.
		for _, suffix := range []string{"", "-wal", "-shm"} {
			src := a.Config.AppDBPath + suffix
			if _, err := os.Stat(src); err == nil {
				entries = append(entries, zipEntry{src, "wandersort.db" + suffix})
			}
		}
		if len(entries) == 1 {
			a.Log.Warn("database not found; packaging logs only", "path", a.Config.AppDBPath)
		}
	}

	zipName := fmt.Sprintf("wandersort-issue-%s.zip", time.Now().Format("20060102-150405"))
	zf, err := os.Create(zipName)
	if err != nil {
		return fmt.Errorf("create zip: %w", err)
	}
	defer zf.Close()

	zw := zip.NewWriter(zf)

	if w, err := zw.Create("about.txt"); err == nil {
		fmt.Fprintf(w, "wandersort issue report\ncreated: %s\nos: %s/%s\n",
			time.Now().Format(time.RFC3339), runtime.GOOS, runtime.GOARCH)
	}

	for _, e := range entries {
		if err := addFileToZip(zw, e.src, e.name); err != nil {
			zw.Close()
			return fmt.Errorf("add %s: %w", e.name, err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalize zip: %w", err)
	}

	abs, _ := filepath.Abs(zipName)
	fmt.Fprintln(os.Stderr, successStyle.Render("Created "+abs))
	fmt.Fprintln(os.Stderr, labelStyle.Render("Attach this file to your bug report or share it with the maintainer."))
	return nil
}

func addFileToZip(zw *zip.Writer, srcPath, entryName string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	w, err := zw.Create(entryName)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}
