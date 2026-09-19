// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package execute

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// seedApproved writes srcContent to a real file under t.TempDir(), then rows
// an APPROVED virtual_fs_entries entry pointing source_path at it and
// target_path at targetRel. Returns the source path.
func seedApproved(t *testing.T, d *db.DB, id int64, targetRel, srcContent string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), filepath.Base(targetRel))
	if err := os.WriteFile(src, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, d, id, filepath.Dir(src), filepath.Base(src), int64(len(srcContent)))
	dbtest.SeedEntry(t, d, id, src, targetRel, db.StatusApproved)
	return src
}

func rowStatus(t *testing.T, d *db.DB, id int64) (status string, errText *string) {
	t.Helper()
	var row struct {
		Status string  `db:"status"`
		Error  *string `db:"error"`
	}
	if err := d.SQL.Get(&row, `SELECT status, error FROM virtual_fs_entries WHERE file_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	return row.Status, row.Error
}

func TestRunCopiesApprovedFilesAndMarksDone(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "2024/A.jpg", "hello")
	seedApproved(t, d, 2, "2024/B.jpg", "world!")

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 2 || rep.Failed != 0 || rep.Bytes != int64(len("hello")+len("world!")) {
		t.Fatalf("got %+v", rep)
	}
	for _, want := range []string{"2024/A.jpg", "2024/B.jpg"} {
		if _, err := os.Stat(filepath.Join(out, want)); err != nil {
			t.Errorf("missing %s: %v", want, err)
		}
	}
	if status, _ := rowStatus(t, d, 1); status != db.StatusDone {
		t.Errorf("status = %q, want %q", status, db.StatusDone)
	}
}

// TestRunCopiesForwardSlashSourcePath guards the read side of the
// separator normalization: source_path is stored through path.ToSourcePath
// (filepath.ToSlash), so a real run has to convert it back
// (path.FromSourcePath) before touching the filesystem, not use it as-is.
func TestRunCopiesForwardSlashSourcePath(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()

	srcDir := filepath.Join(t.TempDir(), "nested", "dir")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(srcDir, "A.jpg")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, d, 1, srcDir, "A.jpg", 5)
	dbtest.SeedEntry(t, d, 1, filepath.ToSlash(src), "2024/A.jpg", db.StatusApproved)

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 || rep.Failed != 0 {
		t.Fatalf("got %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(out, "2024/A.jpg")); err != nil {
		t.Errorf("missing copy: %v", err)
	}
}

func TestRunCopyNeverTouchesSource(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "A.jpg", "hello")

	if _, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: ModeCopy}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("copy removed the source: %v", err)
	}
}

func TestRunMoveUnlinksSourceOnlyAfterVerifiedCopy(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "A.jpg", "hello")

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: ModeMove})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 {
		t.Fatalf("got %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(out, "A.jpg")); err != nil {
		t.Errorf("destination missing: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source still exists after move: %v", err)
	}
}

func TestRunDryRunTouchesNothing(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "A.jpg", "hello")

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: ModeMove, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 || rep.Bytes != int64(len("hello")) {
		t.Fatalf("dry run should still report real numbers, got %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(out, "A.jpg")); !os.IsNotExist(err) {
		t.Error("dry run wrote a destination file")
	}
	if _, err := os.Stat(src); err != nil {
		t.Error("dry run touched the source")
	}
	if status, _ := rowStatus(t, d, 1); status != db.StatusApproved {
		t.Errorf("dry run changed status to %q", status)
	}
	if _, err := os.Stat(filepath.Join(out, db.BackupFileName)); !os.IsNotExist(err) {
		t.Error("dry run wrote a database backup")
	}
}

func TestRunBacksUpPreRunPlan(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "A.jpg", "hello")

	if _, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{}); err != nil {
		t.Fatal(err)
	}
	b, err := sql.Open("sqlite", filepath.Join(out, db.BackupFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var status string
	if err := b.QueryRow(`SELECT status FROM virtual_fs_entries WHERE file_id = 1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != db.StatusApproved {
		t.Errorf("backup holds status %q, want the pre-run %q", status, db.StatusApproved)
	}
}

func TestRunRecordsErrorAndContinuesPastFailure(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	// row 1's source doesn't exist on disk; row 2 is real and must still run.
	dbtest.SeedFile(t, d, 1, "/no/such/dir", "missing.jpg", 0)
	dbtest.SeedEntry(t, d, 1, "/no/such/dir/missing.jpg", "missing.jpg", db.StatusApproved)
	seedApproved(t, d, 2, "B.jpg", "world")

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 || rep.Failed != 1 {
		t.Fatalf("got %+v", rep)
	}
	status, errText := rowStatus(t, d, 1)
	if status != db.StatusError {
		t.Errorf("status = %q, want %q", status, db.StatusError)
	}
	if errText == nil || *errText == "" {
		t.Error("failed row has no recorded error reason")
	}
	if status, _ := rowStatus(t, d, 2); status != db.StatusDone {
		t.Errorf("row after the failure was not processed: status = %q", status)
	}
}

func TestRunSkipsNonApprovedRows(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := filepath.Join(t.TempDir(), "still-proposed.jpg")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, d, 1, filepath.Dir(src), filepath.Base(src), 1)
	dbtest.SeedEntry(t, d, 1, src, "still-proposed.jpg", db.StatusProposed)

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 0 || rep.Failed != 0 {
		t.Fatalf("a PROPOSED row was touched: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(out, "still-proposed.jpg")); !os.IsNotExist(err) {
		t.Error("a PROPOSED row was written to the output")
	}
}

func targetPath(t *testing.T, d *db.DB, id int64) string {
	t.Helper()
	var p string
	if err := d.SQL.Get(&p, `SELECT target_path FROM virtual_fs_entries WHERE file_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	return p
}

// A file already on disk at the planned destination — one the database
// doesn't know about — is never replaced: the transfer takes the next free
// _N name and the row records where it really landed.
func TestRunNeverOverwritesExistingDestination(t *testing.T) {
	for _, mode := range []Mode{ModeCopy, ModeMove} {
		t.Run(mode.String(), func(t *testing.T) {
			d := dbtest.New(t)
			out := t.TempDir()
			src := seedApproved(t, d, 1, "2024/A.jpg", "incoming")
			if err := os.MkdirAll(filepath.Join(out, "2024"), 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"A.jpg": "already here", "A_1.jpg": "also here"} {
				if err := os.WriteFile(filepath.Join(out, "2024", name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: mode})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Done != 1 || rep.Failed != 0 {
				t.Fatalf("got %+v", rep)
			}
			for name, want := range map[string]string{"A.jpg": "already here", "A_1.jpg": "also here", "A_2.jpg": "incoming"} {
				got, err := os.ReadFile(filepath.Join(out, "2024", name))
				if err != nil || string(got) != want {
					t.Errorf("%s = %q, %v; want %q", name, got, err, want)
				}
			}
			if got := targetPath(t, d, 1); got != "2024/A_2.jpg" {
				t.Errorf("target_path = %q, want 2024/A_2.jpg", got)
			}
			_, err = os.Stat(src)
			if mode == ModeCopy && err != nil {
				t.Errorf("copy removed the source: %v", err)
			}
			if mode == ModeMove && !os.IsNotExist(err) {
				t.Errorf("move left the source behind: %v", err)
			}
		})
	}
}

// A move that can't land anywhere keeps its source.
func TestRunMoveKeepsSourceWhenNothingLanded(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("root and Windows ignore the read-only output folder this test relies on")
	}
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "ro/A.jpg", "incoming")
	// Only the target folder is read-only: the output folder itself has to
	// take the database backup, or the run stops before trying anything.
	ro := filepath.Join(out, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) })

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: ModeMove})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 {
		t.Fatalf("got %+v", rep)
	}
	if got, err := os.ReadFile(src); err != nil || string(got) != "incoming" {
		t.Errorf("source = %q, %v; want it untouched", got, err)
	}
}

// After a transfer lands, the row's own source_path and the file's registry
// entry point at the library, not the source — the same relative value as
// target_path (spec D9/D10).
func TestRunRepointsToLibraryRelativePath(t *testing.T) {
	for _, mode := range []Mode{ModeCopy, ModeMove} {
		t.Run(mode.String(), func(t *testing.T) {
			d := dbtest.New(t)
			out := t.TempDir()
			seedApproved(t, d, 1, "2024/A.jpg", "hello")

			if _, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: mode}); err != nil {
				t.Fatal(err)
			}

			var row struct {
				SourcePath string `db:"source_path"`
				TargetPath string `db:"target_path"`
			}
			if err := d.SQL.Get(&row, `SELECT source_path, target_path FROM virtual_fs_entries WHERE file_id = 1`); err != nil {
				t.Fatal(err)
			}
			if row.SourcePath != row.TargetPath {
				t.Errorf("source_path = %q, want it to equal target_path %q", row.SourcePath, row.TargetPath)
			}
			if filepath.IsAbs(row.SourcePath) {
				t.Errorf("source_path = %q, want library-relative", row.SourcePath)
			}

			var reg struct {
				FileDir  string `db:"file_dir"`
				FileName string `db:"file_name"`
				Placed   bool   `db:"placed"`
			}
			if err := d.SQL.Get(&reg, `SELECT file_dir, file_name, placed FROM file_registry WHERE id = 1`); err != nil {
				t.Fatal(err)
			}
			if got := reg.FileDir + "/" + reg.FileName; got != row.TargetPath {
				t.Errorf("file_registry points at %q, want %q", got, row.TargetPath)
			}
			if !reg.Placed {
				t.Error("placed = false after a successful transfer, want true")
			}
		})
	}
}

// seedHashedFile writes a file_registry row with a file_metadata hash but no
// virtual_fs_entries row — the shape of a duplicate the scorer never elected
// (is_master = 0, never proposed) or a copy of an already-placed file a
// later scan saw again.
func seedHashedFile(t *testing.T, d *db.DB, id int64, dir, name, hash string) {
	t.Helper()
	dbtest.SeedFile(t, d, id, dir, name, 5)
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO file_metadata (file_hash, file_id) VALUES (?, ?)`, hash, id); err != nil {
		t.Fatal(err)
	}
}

func TestRunCleansUpDuplicatesOfPlacedFiles(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "A.jpg", "hello")
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO file_metadata (file_hash, file_id) VALUES ('shared-hash', 1)`); err != nil {
		t.Fatal(err)
	}
	// A loser duplicate: same hash, never proposed
	seedHashedFile(t, d, 2, "/backup", "dupe.jpg", "shared-hash")
	// An unrelated file: different hash, must survive
	seedHashedFile(t, d, 3, "/backup", "other.jpg", "other-hash")

	if _, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := d.SQL.Get(&count, `SELECT COUNT(*) FROM file_registry WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("duplicate of a placed file survived execute's cleanup")
	}
	if err := d.SQL.Get(&count, `SELECT COUNT(*) FROM file_metadata WHERE file_id = 2`); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("duplicate's metadata row survived execute's cleanup")
	}
	if err := d.SQL.Get(&count, `SELECT COUNT(*) FROM file_registry WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Error("unrelated file was removed by execute's cleanup")
	}
}

// An ERROR row (a file execute itself couldn't place) is never a cleanup
// target: only one file per hash is ever a master, so an ERROR row's hash
// can't also belong to a DONE row.
func TestRunCleanupLeavesErrorRowsAlone(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "A.jpg", "hello")
	// row 2's source doesn't exist on disk, so it lands at ERROR
	dbtest.SeedFile(t, d, 2, "/no/such/dir", "missing.jpg", 0)
	dbtest.SeedEntry(t, d, 2, "/no/such/dir/missing.jpg", "missing.jpg", db.StatusApproved)
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO file_metadata (file_hash, file_id) VALUES ('missing-hash', 2)`); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{}); err != nil {
		t.Fatal(err)
	}

	status, _ := rowStatus(t, d, 2)
	if status != db.StatusError {
		t.Fatalf("status = %q, want %q", status, db.StatusError)
	}
	var reg struct {
		Count  int  `db:"count"`
		Placed bool `db:"placed"`
	}
	if err := d.SQL.Get(&reg, `SELECT COUNT(*) AS count, COALESCE(MAX(placed), 0) AS placed FROM file_registry WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if reg.Count != 1 {
		t.Error("ERROR row was removed by execute's cleanup")
	}
	if reg.Placed {
		t.Error("placed = true on a row that failed to transfer, want false")
	}
}

func TestWithSuffix(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"a/IMG.jpg", 0, "a/IMG.jpg"},
		{"a/IMG.jpg", 1, "a/IMG_1.jpg"},
		{"a/IMG.jpg", 12, "a/IMG_12.jpg"},
		{"a/README", 2, "a/README_2"},
	} {
		if got := withSuffix(c.in, c.n); got != c.want {
			t.Errorf("withSuffix(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}
