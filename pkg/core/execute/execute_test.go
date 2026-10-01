package execute

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/klauspost/compress/zstd"

	"github.com/jammutkarsh/wandersort/pkg/core/metadata"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
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
	// the date SeedFile records, so the source reads as unchanged since the scan
	scanned := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(src, scanned, scanned); err != nil {
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

	if _, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("copy removed the source: %v", err)
	}
}

func TestRunDryRunTouchesNothing(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "A.jpg", "hello")

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{DryRun: true})
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

// A placed file is decided and a re-run leaves it alone; a failed transfer is
// tried again, so the file still reaches the library.
func TestRunSkipsPlacedAndRetriesFailedRows(t *testing.T) {
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
	if rep.Done != 1 || rep.Failed != 0 {
		t.Fatalf("got %+v, want only the failed file transferred", rep)
	}
	if _, err := os.Stat(filepath.Join(out, "placed.jpg")); !os.IsNotExist(err) {
		t.Error("a placed file was written to the output again")
	}
	if status, _ := rowStatus(t, d, 2); status != statePlaced {
		t.Errorf("failed file after a re-run: status = %q, want %q", status, statePlaced)
	}
	if got := targetPath(t, d, 2); got != "2024/failed.jpg" {
		t.Errorf("retried row's target = %q, want the planned one", got)
	}
}

// Pending counts every planned file not yet placed, failed ones included
// (execute retries them), and nothing in an empty library. A dry run reports
// the same and leaves the failed one failed.
func TestPendingCountsWhatRunWouldTransfer(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	if files, bytes, err := Pending(ctx, d); err != nil || files != 0 || bytes != 0 {
		t.Fatalf("empty library: %d files, %d bytes, %v; want nothing", files, bytes, err)
	}
	seedApproved(t, d, 1, "placed.jpg", "placed")
	dbtest.SeedPlaced(t, d, 1)
	seedApproved(t, d, 2, "failed.jpg", "failed")
	dbtest.SeedTransferError(t, d, 2, "earlier failure")
	seedApproved(t, d, 3, "a.jpg", "abc")
	seedApproved(t, d, 4, "b.jpg", "defgh")

	files, bytes, err := Pending(ctx, d)
	if err != nil || files != 3 || bytes != 14 {
		t.Fatalf("Pending = %d files, %d bytes, %v; want 3 files, 14 bytes", files, bytes, err)
	}
	rep, err := Run(ctx, d, logger.NewNoopLogger(), t.TempDir(), Options{DryRun: true})
	if err != nil || rep.Done != files || rep.Bytes != bytes {
		t.Errorf("dry run = %+v, %v; want Pending's %d files, %d bytes", rep, err, files, bytes)
	}
	if status, _ := rowStatus(t, d, 2); status != stateFailed {
		t.Errorf("dry run changed the failed file: status = %q", status)
	}
}

// A failure records op and kind, and a later success in the same store
// (markResult) removes the row — errors only ever holds live problems.
func TestMarkResultRecordsAndClearsError(t *testing.T) {
	d := dbtest.New(t)
	seedApproved(t, d, 1, "A.jpg", "hello")

	if err := markFailed(context.Background(), d, 1, &stepError{opStat, fmt.Errorf("source missing: %w", fs.ErrNotExist)}); err != nil {
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

	if err := markPlaced(context.Background(), d, 1, 1, "A.jpg"); err != nil {
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
	if err := markPlaced(context.Background(), d, 1, 1, "A.jpg"); err == nil {
		t.Error("markPlaced returned nil after the writer was closed")
	}
	if err := markFailed(context.Background(), d, 1, fmt.Errorf("boom")); err == nil {
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

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
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
	if _, err := os.Stat(src); err != nil {
		t.Errorf("copy removed the source: %v", err)
	}
}

// After a transfer lands, the row's source_path and the registry entry point at
// the library: the same relative value as target_path.
func TestRunRepointsToLibraryRelativePath(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "2024/A.jpg", "hello")

	if _, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{}); err != nil {
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
	if _, err := d.SQL.ExecContext(context.Background(),
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

// A source changed after the scan (same size, different bytes) is not the
// planned file: nothing lands, the source stays, the row records both hashes,
// and the run goes on.
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
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "2024/A.jpg", "hello")
	if err := os.MkdirAll(filepath.Join(out, "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "2024", "A.jpg"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
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
	if _, err := os.Stat(src); err != nil {
		t.Errorf("copy removed the source: %v", err)
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
	err := place(src, dst, hashOf("hello"), func() error {
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
	if _, err := os.Stat(dst); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an unrecorded copy was left in the library: %v", err)
	}
}

// Stopping between files leaves every file not yet reached pending, says so,
// and records nothing about it.
func TestRunStopsBetweenFilesWhenCancelled(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "2024/A.jpg", "hello")
	seedApproved(t, d, 2, "2024/B.jpg", "world")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep, err := Run(ctx, d, logger.NewNoopLogger(), out, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rep.Done != 0 || rep.Failed != 0 {
		t.Errorf("report = %+v, want nothing touched", rep)
	}
	for _, id := range []int64{1, 2} {
		if status, _ := rowStatus(t, d, id); status != statePending {
			t.Errorf("row %d = %q, want %q", id, status, statePending)
		}
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

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
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

// A landed file is found past a gap in the _N names: a deleted _1 does not
// hide the file at _2, and the one at _1's neighbour with the wrong size is
// never taken for it.
func TestRunRecordsLandedFilePastAGap(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	src := seedApproved(t, d, 1, "2024/A.jpg", "hello")
	if err := os.MkdirAll(filepath.Join(out, "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"A.jpg":   "someone else's photo",
		"A_2.jpg": "hello",
	} {
		if err := os.WriteFile(filepath.Join(out, "2024", name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Done != 1 || rep.Failed != 0 {
		t.Fatalf("report = %+v, want the file at A_2.jpg recorded", rep)
	}
	if got := targetPath(t, d, 1); got != "2024/A_2.jpg" {
		t.Errorf("target = %q, want 2024/A_2.jpg", got)
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

// Forgetting a duplicate is only safe while the file it duplicates is
// actually in the library. If the placed file lost its bytes, the database's
// knowledge of every surviving copy is the only record left of them — and
// there is nothing else that remembers.
func TestCleanupKeepsDuplicatesWhenThePlacedFileIsGone(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "A.jpg", "hello")
	seedHashedFile(t, d, 2, "/backup", "dupe.jpg", hashOf("hello"))

	if _, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{}); err != nil {
		t.Fatal(err)
	}
	// The duplicate is gone, as it should be while the library holds the file.
	var count int
	if err := d.SQL.Get(&count, `SELECT COUNT(*) FROM file_registry WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("duplicate of an intact placed file survived the cleanup")
	}

	// Now the same situation with the placed file missing from disk.
	seedHashedFile(t, d, 3, "/backup", "dupe2.jpg", hashOf("hello"))
	if err := os.Remove(filepath.Join(out, "A.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := cleanupPlacedDuplicates(context.Background(), d, out); err != nil {
		t.Fatal(err)
	}
	if err := d.SQL.Get(&count, `SELECT COUNT(*) FROM file_registry WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Error("the cleanup forgot a duplicate of a file that is no longer in the library")
	}
}

// LeftBehind names exactly the files no transfer will bring in: never read,
// whether a read failed or never happened. A read file (planned or a
// duplicate) and a placed one are accounted for.
func TestLeftBehindNamesNeverReadFiles(t *testing.T) {
	d := dbtest.New(t)
	seedApproved(t, d, 1, "read.jpg", "read")
	dbtest.SeedFile(t, d, 2, "/card/DCIM", "failed.jpg", 1)
	dbtest.SeedFile(t, d, 3, "/card/DCIM", "unread.jpg", 1)
	if err := d.Writer.WriteSync(context.Background(), func(ctx context.Context, tx *sqlx.Tx) error {
		return db.RecordError(ctx, tx, 2, db.StageRead, "open", errors.New("reader dropped out"))
	}); err != nil {
		t.Fatal(err)
	}
	seedApproved(t, d, 4, "placed.jpg", "placed")
	dbtest.SeedPlaced(t, d, 4)

	got, err := LeftBehind(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join("/card/DCIM", "failed.jpg"), filepath.Join("/card/DCIM", "unread.jpg")}
	if !slices.Equal(got, want) {
		t.Errorf("LeftBehind = %v, want %v", got, want)
	}
}

// seedPlanWithRename plans one file under 2024/06_June/03 and leaves a review
// draft in out renaming the day to Goa-Trip. Returns the entry's id.
func seedPlanWithRename(t *testing.T, d *db.DB, out string) int64 {
	t.Helper()
	seedApproved(t, d, 1, "2024/06_June/03/a.jpg", "photo")
	tree, err := vfs.BuildTree(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := vfs.OpenDraft(out, tree)
	if err != nil {
		t.Fatal(err)
	}
	day := tree[0].Children[0].Children[0]
	if _, err := draft.Apply(vfs.Edit{Op: vfs.OpRename, Node: day.ID, From: day.Name, To: "Goa-Trip"}); err != nil {
		t.Fatal(err)
	}
	return 1
}

// Spec D18 from Run's side: the review's edits reach the plan before any file
// moves, the file lands under the renamed folder, the draft is gone, and
// OnApplied runs once the plan is written.
func TestRunAppliesTheReviewDraftFirst(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	id := seedPlanWithRename(t, d, out)
	applied := 0
	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{OnApplied: func() { applied++ }})
	if err != nil || rep.Done != 1 {
		t.Fatalf("Run = %+v, %v; want one file placed", rep, err)
	}
	if applied != 1 {
		t.Errorf("OnApplied ran %d times, want once", applied)
	}
	if _, err := os.Stat(filepath.Join(out, "2024/06_June/Goa-Trip/a.jpg")); err != nil {
		t.Errorf("file not under the renamed folder: %v", err)
	}
	if got := targetPath(t, d, id); got != "2024/06_June/Goa-Trip/a.jpg" {
		t.Errorf("target = %q, want the renamed folder", got)
	}
	if _, err := os.Stat(filepath.Join(out, vfs.DraftFileName)); !os.IsNotExist(err) {
		t.Errorf("draft still there after Run: %v", err)
	}
}

// A dry run reports the paths the real run will use — the review's edits
// applied — and leaves the plan, the draft and the output as they were.
func TestRunDryRunReportsEditedPaths(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	id := seedPlanWithRename(t, d, out)
	var targets []string
	rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{
		DryRun:     true,
		OnApplied:  func() { t.Error("OnApplied must not run on a dry run") },
		OnProgress: func(target string, _ int64, _, _ int) { targets = append(targets, target) },
	})
	if err != nil || rep.Done != 1 {
		t.Fatalf("dry run = %+v, %v; want one file", rep, err)
	}
	if want := []string{"2024/06_June/Goa-Trip/a.jpg"}; !slices.Equal(targets, want) {
		t.Errorf("dry run reported %v, want %v", targets, want)
	}
	if got := targetPath(t, d, id); got != "2024/06_June/03/a.jpg" {
		t.Errorf("a dry run wrote the plan: target = %q", got)
	}
	if _, err := os.Stat(filepath.Join(out, vfs.DraftFileName)); err != nil {
		t.Errorf("a dry run removed the draft: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "2024")); !os.IsNotExist(err) {
		t.Errorf("a dry run wrote to the output: %v", err)
	}
}

// A plan the output can't hold is refused before anything changes: the
// draft stays unapplied, no backup is written, no file moves.
func TestRunRefusesPlanThatDoesNotFit(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	id := seedPlanWithRename(t, d, out)
	_, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{
		OnApplied: func() { t.Error("OnApplied ran on a refused plan") },
		freeSpace: func(string) (uint64, uint64, error) { return 1 << 20, 1 << 30, nil },
	})
	var full *NotEnoughSpaceError
	if !errors.As(err, &full) || full.Free != 1<<20 || full.Files != 5 || full.Needed <= full.Free {
		t.Fatalf("err = %v, want a NotEnoughSpaceError with the figures", err)
	}
	if !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("message %q should say nothing changed", err)
	}
	if got := targetPath(t, d, id); got != "2024/06_June/03/a.jpg" {
		t.Errorf("a refused plan was edited: target = %q", got)
	}
	if _, err := os.Stat(filepath.Join(out, vfs.DraftFileName)); err != nil {
		t.Errorf("draft gone after a refusal: %v", err)
	}
	for _, name := range []string{db.BackupFileName, "2024"} {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Errorf("%s written by a refused plan: %v", name, err)
		}
	}
}

// An unreadable free-space figure doesn't block the transfer: each file still
// lands whole or not at all.
func TestRunTransfersWhenFreeSpaceIsUnknown(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "2024/06_June/03/a.jpg", "photo")
	statfsFails := func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs failed") }
	if rep, err := Run(context.Background(), d, logger.NewNoopLogger(), out, Options{freeSpace: statfsFails}); err != nil || rep.Done != 1 {
		t.Fatalf("Run = %+v, %v; want the file placed", rep, err)
	}
}

// The space check counts only what is still to be copied: files already in the
// library take no new room.
func TestCheckFitsIgnoresPlacedFiles(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	seedApproved(t, d, 1, "2024/a.jpg", "pending")
	dbtest.SeedFile(t, d, 2, "2024", "big.jpg", 500<<30)
	dbtest.SeedEntry(t, d, 2, "2024/big.jpg", "2024/big.jpg")
	dbtest.SeedPlaced(t, d, 2)

	plenty := func(string) (uint64, uint64, error) { return 10 << 30, 100 << 30, nil }
	if err := checkFits(context.Background(), d, out, plenty); err != nil {
		t.Errorf("checkFits = %v; a placed 500 GiB file must not count", err)
	}
}
