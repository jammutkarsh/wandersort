// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package execute

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/jammutkarsh/wandersort/pkg/core/metadata"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// seedApproved writes srcContent to a real file under t.TempDir(), then rows
// an APPROVED virtual_fs_entries entry pointing source_path at it and
// target_path at targetRel, with the hash the scan would have stored. Returns
// the source path.
func seedApproved(t *testing.T, d *db.DB, id int64, targetRel, srcContent string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), filepath.Base(targetRel))
	if err := os.WriteFile(src, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, d, id, filepath.Dir(src), filepath.Base(src), int64(len(srcContent)))
	dbtest.SeedEntry(t, d, id, src, targetRel)
	dbtest.SeedHash(t, d, id, hashOf(srcContent))
	return src
}

func hashOf(content string) string {
	h := metadata.NewHasher()
	h.Write([]byte(content))
	return metadata.HashString(h)
}

// Where a planned file stands: there is no status column, so it is placed once
// file_registry says so, failed while it has a TRANSFER error, pending otherwise.
const (
	statePending = "pending"
	statePlaced  = "placed"
	stateFailed  = "failed"
)

func rowStatus(t *testing.T, d *db.DB, id int64) (status string, errText *string) {
	t.Helper()
	var placed bool
	if err := d.SQL.Get(&placed, `SELECT placed FROM file_registry WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var detail string
	err := d.SQL.Get(&detail, `SELECT detail FROM errors WHERE file_id = ? AND stage = ?`, id, db.StageTransfer)
	switch {
	case placed:
		return statePlaced, nil
	case err == nil:
		return stateFailed, &detail
	}
	return statePending, nil
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
	if status, _ := rowStatus(t, d, 1); status != statePlaced {
		t.Errorf("status = %q, want %q", status, statePlaced)
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
	dbtest.SeedEntry(t, d, 1, filepath.ToSlash(src), "2024/A.jpg")
	dbtest.SeedHash(t, d, 1, hashOf("hello"))

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
	if status, _ := rowStatus(t, d, 1); status != statePending {
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
	bak, err := os.Open(filepath.Join(out, db.BackupFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer bak.Close()
	zr, err := zstd.NewReader(bak)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	plain := filepath.Join(t.TempDir(), "backup.db")
	pf, err := os.Create(plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(pf, zr); err != nil {
		t.Fatal(err)
	}
	pf.Close()
	b, err := sql.Open("sqlite", plain)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var placed bool
	if err := b.QueryRow(`SELECT placed FROM file_registry WHERE id = 1`).Scan(&placed); err != nil {
		t.Fatal(err)
	}
	if placed {
		t.Error("backup holds the file as placed, want the pre-run plan")
	}
}

func TestRunRecordsErrorAndContinuesPastFailure(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	// row 1's source doesn't exist on disk; row 2 is real and must still run.
	dbtest.SeedFile(t, d, 1, "/no/such/dir", "missing.jpg", 0)
	dbtest.SeedEntry(t, d, 1, "/no/such/dir/missing.jpg", "missing.jpg")
	seedApproved(t, d, 2, "B.jpg", "world")

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 || rep.Failed != 1 {
		t.Fatalf("got %+v", rep)
	}
	status, errText := rowStatus(t, d, 1)
	if status != stateFailed {
		t.Errorf("status = %q, want %q", status, stateFailed)
	}
	if errText == nil || *errText == "" {
		t.Error("failed row has no recorded error reason")
	}
	if status, _ := rowStatus(t, d, 2); status != statePlaced {
		t.Errorf("row after the failure was not processed: status = %q", status)
	}
}

// A placed file and a failed one are both decided: a re-run touches neither,
// and the failed row keeps the folder it was planned into.
func TestRunSkipsPlacedAndFailedRows(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "placed.jpg", "x")
	dbtest.SeedPlaced(t, d, 1)
	seedApproved(t, d, 2, "2024/failed.jpg", "y")
	dbtest.SeedTransferError(t, d, 2, "earlier failure")

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 0 || rep.Failed != 0 {
		t.Fatalf("a decided row was touched: %+v", rep)
	}
	for _, name := range []string{"placed.jpg", "2024/failed.jpg"} {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Errorf("%s was written to the output", name)
		}
	}
	if got := targetPath(t, d, 2); got != "2024/failed.jpg" {
		t.Errorf("failed row's target = %q, want the planned one", got)
	}
}

// A failure records op and kind, and a later success in the same store
// (markResult) removes the row — errors only ever holds live problems.
func TestMarkResultRecordsAndClearsError(t *testing.T) {
	d := dbtest.New(t)
	seedApproved(t, d, 1, "A.jpg", "hello")

	if err := markFailed(d, 1, &stepError{opStat, fmt.Errorf("source missing: %w", fs.ErrNotExist)}); err != nil {
		t.Fatal(err)
	}
	var row struct {
		Op   string `db:"op"`
		Kind string `db:"kind"`
	}
	if err := d.SQL.Get(&row, `SELECT op, kind FROM errors WHERE file_id = 1 AND stage = 'TRANSFER'`); err != nil {
		t.Fatal(err)
	}
	if row.Op != opStat || row.Kind != db.KindNotFound {
		t.Errorf("error row = %+v, want op stat, kind not-found", row)
	}

	if err := markPlaced(d, 1, 1, "A.jpg"); err != nil {
		t.Fatal(err)
	}
	if status, _ := rowStatus(t, d, 1); status != statePlaced {
		t.Errorf("state = %s, want placed with no error row left", status)
	}
}

// A write the user pays for in photos must not be reportable as success when
// it failed: both marks return the transaction's own error rather than
// dropping it into a log line, and both are already durable when they return
// (no Flush needed to read them back).
func TestMarkPlacedReportsItsError(t *testing.T) {
	d := dbtest.New(t)
	seedApproved(t, d, 1, "A.jpg", "hello")

	// file_id 99 has no file_registry row, so the UPDATE matches nothing and
	// the DELETE below it is fine — the failure we can force is a closed
	// writer, which is what a shutdown mid-run looks like.
	d.Writer.Close()
	if err := markPlaced(d, 1, 1, "A.jpg"); err == nil {
		t.Error("markPlaced returned nil after the writer was closed")
	}
	if err := markFailed(d, 1, fmt.Errorf("boom")); err == nil {
		t.Error("markFailed returned nil after the writer was closed")
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
	dbtest.SeedHash(t, d, id, hash)
}

func TestRunCleansUpDuplicatesOfPlacedFiles(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "A.jpg", "hello")
	// A loser duplicate: same hash, never proposed
	seedHashedFile(t, d, 2, "/backup", "dupe.jpg", hashOf("hello"))
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
	dbtest.SeedEntry(t, d, 2, "/no/such/dir/missing.jpg", "missing.jpg")
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO file_metadata (file_hash, file_id) VALUES ('missing-hash', 2)`); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{}); err != nil {
		t.Fatal(err)
	}

	status, _ := rowStatus(t, d, 2)
	if status != stateFailed {
		t.Fatalf("status = %q, want %q", status, stateFailed)
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

// Spec D22: a source changed after the scan (same size, different bytes) is
// not the file that was planned. Nothing lands, the source stays, the row
// says why with both hashes, and the run goes on to the next file.
func TestRunRefusesSourceChangedSinceScan(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "2024/A.jpg", "hello")
	if err := os.WriteFile(src, []byte("HELLO"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedApproved(t, d, 2, "2024/B.jpg", "world")

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 || rep.Failed != 1 {
		t.Fatalf("got %+v", rep)
	}
	status, errText := rowStatus(t, d, 1)
	if status != stateFailed {
		t.Errorf("status = %q, want %q", status, stateFailed)
	}
	if errText == nil || !strings.Contains(*errText, hashOf("hello")) || !strings.Contains(*errText, hashOf("HELLO")) {
		t.Errorf("error = %v, want both hashes in it", errText)
	}
	entries, _ := os.ReadDir(filepath.Join(out, "2024"))
	if len(entries) != 1 || entries[0].Name() != "B.jpg" {
		t.Errorf("2024/ holds %v, want only B.jpg (no A.jpg, no temp file)", entries)
	}
	if got, err := os.ReadFile(src); err != nil || string(got) != "HELLO" {
		t.Errorf("source = %q, %v; want it untouched", got, err)
	}
	if status, _ := rowStatus(t, d, 2); status != statePlaced {
		t.Errorf("row after the mismatch: status = %q, want %q", status, statePlaced)
	}
}

// A taken name already holding this very file — an earlier copy the crash
// or `recover` left unrecorded — is where the file is: no second copy at _1.
func TestRunRecognisesFileAlreadyPlaced(t *testing.T) {
	for _, mode := range []Mode{ModeCopy, ModeMove} {
		t.Run(mode.String(), func(t *testing.T) {
			d := dbtest.New(t)
			out := t.TempDir()
			src := seedApproved(t, d, 1, "2024/A.jpg", "hello")
			if err := os.MkdirAll(filepath.Join(out, "2024"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, "2024", "A.jpg"), []byte("hello"), 0o644); err != nil {
				t.Fatal(err)
			}

			rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: mode})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Done != 1 || rep.Failed != 0 {
				t.Fatalf("got %+v", rep)
			}
			if got := targetPath(t, d, 1); got != "2024/A.jpg" {
				t.Errorf("target_path = %q, want 2024/A.jpg", got)
			}
			if _, err := os.Stat(filepath.Join(out, "2024", "A_1.jpg")); !os.IsNotExist(err) {
				t.Error("placed a second copy at A_1.jpg")
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

// A move whose file is verified in the library but whose source can't be
// removed is still DONE at the landed path: an ERROR row would leave a
// library file the database doesn't know about.
func TestRunMoveRecordsLandedFileWhenSourceKept(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("root and Windows ignore the read-only source folder this test relies on")
	}
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "A.jpg", "hello")
	if err := os.WriteFile(filepath.Join(out, "A.jpg"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(src), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Dir(src), 0o755) })

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: ModeMove})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 {
		t.Fatalf("got %+v", rep)
	}
	if status, _ := rowStatus(t, d, 1); status != statePlaced {
		t.Errorf("status = %q, want %q", status, statePlaced)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source gone: %v", err)
	}
}

// The library copy matching the scan is no reason to delete a source edited
// since (same size): the move stops at ERROR and the edit survives (spec D22).
func TestRunMoveKeepsSourceEditedSinceScanWhenAlreadyPlaced(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "A.jpg", "hello")
	if err := os.WriteFile(filepath.Join(out, "A.jpg"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("HELLO"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: ModeMove})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 {
		t.Fatalf("got %+v", rep)
	}
	if status, _ := rowStatus(t, d, 1); status != stateFailed {
		t.Errorf("status = %q, want %q", status, stateFailed)
	}
	if got, err := os.ReadFile(src); err != nil || string(got) != "HELLO" {
		t.Errorf("source = %q, %v; want the edit kept", got, err)
	}
	if _, err := os.Stat(filepath.Join(out, "A_1.jpg")); !os.IsNotExist(err) {
		t.Error("placed the edited source at A_1.jpg")
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

// The copy path must put the file in place, verified, before it tells the
// database anything — and a commit that fails must be reported rather than
// counted as done. For a move the same commit is what stands between the
// source being unlinked and not; see the cross-device branch of place.
func TestPlaceCommitsOnlyOnceTheFileIsInPlace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "A.jpg")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "lib", "A.jpg")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}

	var sawFile bool
	err := place(ModeCopy, src, dst, hashOf("hello"), func() error {
		_, statErr := os.Stat(dst)
		sawFile = statErr == nil
		return fmt.Errorf("writer closed")
	})
	if err == nil {
		t.Fatal("place swallowed the commit error")
	}
	if !sawFile {
		t.Error("commit ran before the file was at its landing path")
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("copy touched the source: %v", err)
	}
}

// A same-device move is one atomic rename, so a commit that fails afterwards
// cannot lose the file — but it does leave the library holding something no
// row accounts for. The next run has to recognise it and record it, or a
// crash mid-move writes every in-flight file off permanently.
func TestRunMoveWithFailedCommitIsRecoveredByTheNextRun(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "2024/A.jpg", "hello")

	// Force the commit to fail exactly where a rolled-back batch would.
	d.Writer.Close()
	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: ModeMove})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 0 || rep.Failed != 1 {
		t.Errorf("report = %+v, want nothing counted as done", rep)
	}
	if _, err := os.Stat(filepath.Join(out, "2024", "A.jpg")); err != nil {
		t.Fatalf("the file should be in the library: %v", err)
	}
	if _, err := os.Stat(src); err == nil {
		t.Error("the rename should have consumed the source")
	}

	// Same library, a working writer, and the failure cleared the way a retry
	// would: the file is found where it landed and recorded, rather than
	// written off for a source that is gone.
	d.Writer = db.NewBulkWriter(d.SQL, logger.NewNoopLogger())
	if _, err := d.SQL.Exec(`DELETE FROM errors WHERE stage = ?`, db.StageTransfer); err != nil {
		t.Fatal(err)
	}
	rep, err = Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: ModeMove})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 {
		t.Errorf("report = %+v, want the landed file recovered", rep)
	}
	if status, detail := rowStatus(t, d, 1); status != statePlaced {
		t.Errorf("status = %q (%v), want %q", status, detail, statePlaced)
	}
}

// A crash after the file landed but before its row committed leaves a missing
// source and a file sitting correctly in the library. That must be recorded,
// not written off as a permanent TRANSFER failure that is never retried —
// which for a move is every file the last run had in flight.
func TestRunRecordsLandedFileWhoseSourceIsGone(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "2024/A.jpg", "hello")

	// The shape the previous run left behind: file in the library, source
	// unlinked, row still pending.
	if err := os.MkdirAll(filepath.Join(out, "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "2024", "A.jpg"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{Mode: ModeMove})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 || rep.Failed != 0 {
		t.Errorf("report = %+v, want the landed file counted as done", rep)
	}
	if status, detail := rowStatus(t, d, 1); status != statePlaced {
		t.Errorf("status = %q (%v), want %q", status, detail, statePlaced)
	}
}

// A missing source with nothing in the library is still a plain failure: the
// reconcile above must not swallow a file the user actually deleted.
func TestRunFailsWhenSourceIsGoneAndNothingLanded(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "2024/A.jpg", "hello")
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 0 || rep.Failed != 1 {
		t.Errorf("report = %+v, want one failure", rep)
	}
	if status, _ := rowStatus(t, d, 1); status != stateFailed {
		t.Errorf("status = %q, want %q", status, stateFailed)
	}
}
