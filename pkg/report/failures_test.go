package report

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

func seedError(t *testing.T, d *db.DB, fileID int64, stage, op, kind string) {
	t.Helper()
	if _, err := d.SQL.ExecContext(context.Background(), `
		INSERT INTO errors (file_id, stage, op, kind, detail, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, '{}', '2024-01-01T00:00:00.000000000Z', '2024-01-01T00:00:00.000000000Z')`,
		fileID, stage, op, kind); err != nil {
		t.Fatal(err)
	}
}

// Failed transfers and never-read files are listed by drive and reason; copied, placed and duplicate files aren't.
func TestFailures(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)

	// SD card: two changed files, one gone
	for id, name := range map[int64]string{1: "IMG_1.JPG", 2: "IMG_2.JPG", 3: "IMG_3.JPG"} {
		dbtest.SeedFile(t, d, id, "/Volumes/SD/DCIM", name, 100)
		dbtest.SeedHash(t, d, id, "md5:"+name)
		dbtest.SeedEntry(t, d, id, "/Volumes/SD/DCIM/"+name, "2024/01_January/02/"+name)
	}
	seedError(t, d, 1, db.StageTransfer, "copy", db.KindChecksumMismatch)
	seedError(t, d, 2, db.StageTransfer, "copy", db.KindChecksumMismatch)
	seedError(t, d, 3, db.StageTransfer, "stat", db.KindNotFound)
	// home disk: one unreadable (no metadata, READ error), one never tried
	dbtest.SeedFile(t, d, 4, "/home/<user>/Pictures", "bad.HEIC", 50)
	seedError(t, d, 4, db.StageRead, "open", db.KindPermissionDenied)
	dbtest.SeedFile(t, d, 5, "/home/<user>/Pictures", "later.HEIC", 50)
	// not failures: a copied file and a duplicate that was never planned
	dbtest.SeedFile(t, d, 6, "/Volumes/SD/DCIM", "ok.JPG", 100)
	dbtest.SeedHash(t, d, 6, "md5:ok")
	dbtest.SeedPlaced(t, d, 6)
	dbtest.SeedFile(t, d, 7, "/Volumes/SD/DCIM", "dup.JPG", 100)
	dbtest.SeedHash(t, d, 7, "md5:IMG_1.JPG")

	r, err := Failures(ctx, d.SQL)
	if err != nil {
		t.Fatal(err)
	}
	if r.NotCopied != 3 || r.NotRead != 2 {
		t.Fatalf("NotCopied, NotRead = %d, %d, want 3, 2", r.NotCopied, r.NotRead)
	}
	got := map[string]map[string]int{}
	for _, drive := range r.Drives {
		got[drive.Drive] = map[string]int{}
		for _, g := range drive.Groups {
			got[drive.Drive][g.Reason] = len(g.Files)
		}
	}
	want := map[string]map[string]int{
		"SD":            {"Changed since it was read": 2, "Original is gone": 1},
		"This computer": {"Not allowed to read or write it": 1, "Not read yet": 1},
	}
	for drive, reasons := range want {
		for reason, n := range reasons {
			if got[drive][reason] != n {
				t.Errorf("%s / %s = %d files, want %d (got %v)", drive, reason, got[drive][reason], n, got)
			}
		}
	}
	if f := r.Drives[0].Groups[0].Files[0]; f.Target != filepath.FromSlash("2024/01_January/02") {
		t.Errorf("planned folder = %q, want 2024/01_January/02", f.Target)
	}

	var page bytes.Buffer
	if err := WriteHTML(&page, r, PageInfo{Library: "/lib", When: time.Now(), LogPath: "/logs/x.log"}); err != nil {
		t.Fatal(err)
	}
	html := page.String()
	for _, s := range []string{"3 files weren&#39;t copied, 2 weren&#39;t read", "IMG_3.JPG", "Original is gone", "This computer"} {
		if !strings.Contains(html, s) {
			t.Errorf("page is missing %q", s)
		}
	}
}

// Failures read by reason gather each reason's files across drives, reasons in
// the order first met.
func TestFailureReportByReason(t *testing.T) {
	r := FailureReport{Drives: []DriveFailures{
		{Drive: "a", Groups: []FailureGroup{{Reason: "full", Files: []FailedFile{{Source: "a/1"}}}, {Reason: "gone", Files: []FailedFile{{Source: "a/2"}}}}},
		{Drive: "b", Groups: []FailureGroup{{Reason: "gone", Files: []FailedFile{{Source: "b/1"}}}}},
	}}
	got := r.ByReason()
	if len(got) != 2 || got[0].Reason != "full" || got[1].Reason != "gone" || len(got[1].Files) != 2 || got[1].Files[1].Source != "b/1" {
		t.Errorf("ByReason = %+v, want full then gone, gone holding both drives' files", got)
	}
}
