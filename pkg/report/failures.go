package report

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/db"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// FailedFile is one file a copy left behind.
type FailedFile struct {
	Source string
	Camera string
	Size   int64
	Target string // planned library folder; empty for a file never read
}

// FailureGroup is every failed file on one drive that failed for one reason.
type FailureGroup struct {
	Reason string
	Next   string
	Files  []FailedFile
}

// DriveFailures is every failed file whose original lives on one drive.
type DriveFailures struct {
	Drive  string
	Count  int
	Groups []FailureGroup
}

// FailureReport is every file that should be in the library and isn't: failed transfers and never-read files.
type FailureReport struct {
	Drives    []DriveFailures
	NotCopied int
	NotRead   int
}

// Total is every file the report lists.
func (r FailureReport) Total() int { return r.NotCopied + r.NotRead }

// Reason says in plain words why a file is not in the library and what to do.
func Reason(stage, kind string) (reason, next string) {
	switch {
	case stage == "": // a file never read
		return "Not read yet", "Add its folder again."
	case kind == db.KindChecksumMismatch:
		return "Changed since it was read", "Add its folder again to pick up the change, then copy."
	case kind == db.KindNotFound:
		return "Original is gone", "Plug in the drive it was on, or add the folder it moved to."
	case kind == db.KindPermissionDenied:
		return "Not allowed to read or write it", "Check the folder's permissions, then try again."
	case kind == db.KindNoSpace:
		return "The library's drive filled up", "Free some space, then copy again."
	case kind == db.KindIO:
		return "The drive reported an error", "Check the drive and its cable, then try again."
	case stage == db.StageRead:
		return "Couldn't be read", "Add its folder again; unread files are tried on every add."
	}
	return "Couldn't be copied", "Copy again; the log next to this page has the details."
}

// Failures reads every failed transfer and never-read file, grouped by the original's drive, then reason.
func Failures(ctx context.Context, q Querier) (FailureReport, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT f.file_dir, f.file_name, f.file_size,
		       COALESCE(e.stage, ''), COALESCE(e.kind, ''),
		       TRIM(COALESCE(m.exif_make, '') || ' ' || COALESCE(m.exif_model, '')),
		       COALESCE(v.target_path, '')
		FROM file_registry f
		LEFT JOIN errors e ON e.file_id = f.id AND e.stage IN (?, ?)
		LEFT JOIN file_metadata m ON m.file_id = f.id
		LEFT JOIN virtual_fs_entries v ON v.file_id = f.id
		WHERE f.placed = 0 AND (e.stage = ? OR m.file_id IS NULL)
		ORDER BY f.file_dir, f.file_name`,
		db.StageRead, db.StageTransfer, db.StageTransfer)
	if err != nil {
		return FailureReport{}, fmt.Errorf("read failed files: %w", err)
	}
	defer rows.Close()

	var r FailureReport
	drives := map[string]int{}
	groups := map[[2]string]int{}
	for rows.Next() {
		var dir, name, stage, kind, target string
		var file FailedFile
		if err := rows.Scan(&dir, &name, &file.Size, &stage, &kind, &file.Camera, &target); err != nil {
			return FailureReport{}, fmt.Errorf("read failed files: %w", err)
		}
		file.Source = filepath.Join(wspath.FromSourcePath(dir), name)
		if target != "" {
			file.Target = filepath.Dir(wspath.FromLibrary(target))
		}
		if stage == db.StageTransfer {
			r.NotCopied++
		} else {
			r.NotRead++
		}

		drive := volume.Label(file.Source)
		d, ok := drives[drive]
		if !ok {
			d = len(r.Drives)
			drives[drive] = d
			r.Drives = append(r.Drives, DriveFailures{Drive: drive})
		}
		reason, next := Reason(stage, kind)
		key := [2]string{drive, reason}
		g, ok := groups[key]
		if !ok {
			g = len(r.Drives[d].Groups)
			groups[key] = g
			r.Drives[d].Groups = append(r.Drives[d].Groups, FailureGroup{Reason: reason, Next: next})
		}
		r.Drives[d].Count++
		r.Drives[d].Groups[g].Files = append(r.Drives[d].Groups[g].Files, file)
	}
	if err := rows.Err(); err != nil {
		return FailureReport{}, fmt.Errorf("read failed files: %w", err)
	}
	return r, nil
}

// PageInfo is what the failure page says about the run it belongs to.
type PageInfo struct {
	Library string
	When    time.Time
	LogPath string
}

//go:embed failures.html.tmpl
var failuresPage string

var failuresTemplate = template.Must(template.New("failures").Funcs(template.FuncMap{
	"size": func(n int64) string { return volume.HumanBytes(uint64(max(n, 0))) },
	"base": filepath.Base,
	"dir":  filepath.Dir,
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	},
}).Parse(failuresPage))

// WriteHTML renders the report as one self-contained page.
func WriteHTML(w io.Writer, r FailureReport, info PageInfo) error {
	return failuresTemplate.Execute(w, struct {
		FailureReport
		PageInfo
	}{r, info})
}

// SaveHTML writes the page to path through a temp file, so a crash never leaves half a page.
func SaveHTML(path string, r FailureReport, info PageInfo) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".report-*")
	if err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if err := WriteHTML(tmp, r, info); err != nil {
		tmp.Close()
		return fmt.Errorf("write report: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}
