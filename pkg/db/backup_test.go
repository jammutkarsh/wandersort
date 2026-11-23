package db

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db/migrations"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func TestBackupIsCompressedAndVerified(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	live := filepath.Join(dir, ".wandersort.db")
	d, err := New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	dest := filepath.Join(dir, BackupFileName)

	// Twice: the second run must replace the first backup, not fail on it.
	for range 2 {
		if err := d.Backup(ctx, dest); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	liveBytes, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	bakBytes, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	// Trips if a future change stores the backup uncompressed.
	if bytes.Equal(liveBytes, bakBytes) || len(bakBytes) >= len(liveBytes) {
		t.Errorf("backup is %d bytes, live database %d: want smaller and different", len(bakBytes), len(liveBytes))
	}
	if left, _ := filepath.Glob(dest + "*.tmp"); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}

	plain := filepath.Join(dir, "plain.db")
	if err := decompressFile(t.Context(), dest, plain); err != nil {
		t.Fatal(err)
	}
	b, err := sql.Open("sqlite", plain)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var n int
	if err := b.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
		t.Errorf("backup not readable: n=%d err=%v", n, err)
	}
	var check string
	if err := b.QueryRow(`PRAGMA quick_check`).Scan(&check); err != nil || check != "ok" {
		t.Errorf("quick_check = %q, %v", check, err)
	}
}

func TestRestoreBringsBackBackedUpState(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	live := filepath.Join(dir, ".wandersort.db")
	dest := filepath.Join(dir, BackupFileName)
	d, err := New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('Goa', 'EVENT')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL.ExecContext(ctx, `DELETE FROM user_labels`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	if err := Restore(ctx, dest, live); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("restore consumed the backup: %v", err)
	}
	// the database it replaced is kept, so the restore itself can be undone
	before, err := sql.Open("sqlite", filepath.Join(dir, BeforeRestoreFileName))
	if err != nil {
		t.Fatal(err)
	}
	var kept int
	if err := before.QueryRow(`SELECT count(*) FROM user_labels`).Scan(&kept); err != nil {
		t.Fatalf("replaced database not kept readable: %v", err)
	}
	before.Close()
	if kept != 0 {
		t.Errorf("kept copy has %d labels, want the replaced state's 0", kept)
	}

	d, err = New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatalf("restored database does not open: %v", err)
	}
	defer d.Close()
	var labels, stamps int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM user_labels`).Scan(&labels); err != nil {
		t.Fatal(err)
	}
	if labels != 1 {
		t.Errorf("user_labels = %d rows, want the backed-up 1", labels)
	}
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'wandersort_backup'`).Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 0 {
		t.Error("restored database carries a backup stamp")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Errorf("restore left temp files: %v", left)
	}
	// A restored library backs up again like any other.
	if err := d.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreWithoutBackupLeavesDatabase(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, ".wandersort.db")
	if err := os.WriteFile(live, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), filepath.Join(dir, BackupFileName), live); err == nil {
		t.Fatal("restore with no backup must fail")
	}
	if got, _ := os.ReadFile(live); string(got) != "keep" {
		t.Errorf("live database changed to %q", got)
	}
}

// newBackedUp returns a closed library database at live holding one label,
// and a backup of it at dest.
func newBackedUp(t *testing.T) (live, dest string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	live, dest = filepath.Join(dir, ".wandersort.db"), filepath.Join(dir, BackupFileName)
	d, err := New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.SQL.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('Goa', 'EVENT')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	return live, dest
}

func TestRestoreRefusesWhileDatabaseIsOpen(t *testing.T) {
	ctx := context.Background()
	live, dest := newBackedUp(t)

	// Another program holding the database open, idle — the case a file
	// lock of our own would never see.
	other, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec(`DELETE FROM user_labels`); err != nil {
		t.Fatal(err)
	}

	if err := Restore(ctx, dest, live); !errors.Is(err, ErrInUse) {
		t.Fatalf("Restore with another connection open = %v, want ErrInUse", err)
	}
	var n int
	if err := other.QueryRow(`SELECT count(*) FROM user_labels`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a refused restore changed the database: n=%d err=%v", n, err)
	}

	other.Close()
	if err := Restore(ctx, dest, live); err != nil {
		t.Fatalf("Restore once the other program closed: %v", err)
	}
}

func TestRestoreRefusesForeignBackup(t *testing.T) {
	ctx := context.Background()
	live, dest := newBackedUp(t)
	plain := dest + ".foreign.db"
	foreign, err := sql.Open("sqlite", plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.Exec(`CREATE TABLE x (y)`); err != nil {
		t.Fatal(err)
	}
	foreign.Close()
	if err := compressFile(t.Context(), plain, dest+".foreign"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(live)

	if err := Restore(ctx, dest+".foreign", live); err == nil {
		t.Fatal("restoring a non-wandersort database must fail")
	}
	if after, _ := os.ReadFile(live); !bytes.Equal(before, after) {
		t.Error("a refused restore changed the database")
	}
}

// A truncated or garbage backup is refused, naming the file, before the live
// database is touched, and no temp file is left on the failure path.
func TestRestoreRefusesCorruptBackup(t *testing.T) {
	ctx := context.Background()
	live, dest := newBackedUp(t)
	good, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(live)
	for name, data := range map[string][]byte{
		"truncated": good[:len(good)/2],
		"garbage":   []byte("not zstd at all"),
	} {
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			t.Fatal(err)
		}
		err := Restore(ctx, dest, live)
		if err == nil || !strings.Contains(err.Error(), dest) {
			t.Errorf("%s: Restore = %v, want an error naming %s", name, err, dest)
		}
		if after, _ := os.ReadFile(live); !bytes.Equal(before, after) {
			t.Errorf("%s: a refused restore changed the database", name)
		}
		if left, _ := filepath.Glob(filepath.Join(filepath.Dir(live), "*.tmp")); len(left) != 0 {
			t.Errorf("%s: temp files left: %v", name, left)
		}
	}
}

func TestBackupKeepsPreviousOnFailure(t *testing.T) {
	ctx := context.Background()
	live, dest := newBackedUp(t)
	before, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// A non-empty directory where the plain temp copy goes fails Backup at its
	// leftover-cleanup step, before VACUUM INTO. Only the "previous backup
	// survives a failure" contract is tested; the compress step has no failure
	// injection (not worth a seam).
	if err := os.Mkdir(dest+".db.tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest+".db.tmp", "x"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.Backup(ctx, dest); err == nil {
		t.Fatal("Backup should have failed")
	}
	if after, _ := os.ReadFile(dest); !bytes.Equal(before, after) {
		t.Error("a failed backup damaged the previous one")
	}
}

// A deleted database is the case CheckLibrary sends people to recover for.
func TestRestoreRecreatesDeletedDatabase(t *testing.T) {
	ctx := context.Background()
	live, dest := newBackedUp(t)
	for _, f := range []string{live, live + "-wal", live + "-shm"} {
		os.Remove(f)
	}
	if err := Restore(ctx, dest, live); err != nil {
		t.Fatal(err)
	}
	d, err := New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM user_labels`).Scan(&n); err != nil || n != 1 {
		t.Errorf("user_labels = %d, %v; want 1", n, err)
	}
}

// Opening an existing library that needs a migration backs it up first, to a
// file of its own; a library from a newer build is refused untouched.
func TestOpenBacksUpBeforeMigratingAndRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	live := filepath.Join(dir, ".wandersort.db")
	open := func() (*DB, error) { return New(ctx, live, logger.NewNoopLogger()) }

	d, err := open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, PreMigrationBackupFileName)); !os.IsNotExist(err) {
		t.Fatalf("a fresh database was backed up: %v", err)
	}
	// pretend the newest migration has not run yet
	if _, err := d.SQL.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = (SELECT MAX(version) FROM schema_migrations)`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if d, err := open(); err == nil { // re-running an applied CREATE may fail; the backup must exist either way
		d.Close()
	}
	if _, err := os.Stat(filepath.Join(dir, PreMigrationBackupFileName)); err != nil {
		t.Fatalf("no backup before the upgrade: %v", err)
	}

	os.Remove(live)
	d, err = open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (999)`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, err := open(); !errors.Is(err, migrations.ErrNewerSchema) {
		t.Fatalf("open = %v, want ErrNewerSchema", err)
	}
}

// The library is read with read(), never mapped: a drive dropping under a
// mapped page kills the process with SIGBUS instead of returning an error.
func TestLibraryIsNotMemoryMapped(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), ".wandersort.db"), logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var size int64
	if err := d.SQL.QueryRow(`PRAGMA mmap_size`).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size != 0 {
		t.Errorf("mmap_size = %d, want 0", size)
	}
}

// A stopped backup or restore doesn't wait for a large database to be packed
// or unpacked.
func TestPackingStopsWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	plain, packed := filepath.Join(dir, "plain"), filepath.Join(dir, "packed")
	if err := os.WriteFile(plain, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := compressFile(t.Context(), plain, packed); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := compressFile(ctx, plain, packed+".2"); !errors.Is(err, context.Canceled) {
		t.Errorf("compressFile after cancel = %v, want context.Canceled", err)
	}
	if err := decompressFile(ctx, packed, plain+".2"); !errors.Is(err, context.Canceled) {
		t.Errorf("decompressFile after cancel = %v, want context.Canceled", err)
	}
}

// A restore stopped while it keeps the database it replaces leaves the one an
// earlier restore kept: it is the only undo.
func TestKeepBeforeRestoreStoppedKeepsTheEarlierCopy(t *testing.T) {
	dir := t.TempDir()
	live, kept := filepath.Join(dir, "live.db"), filepath.Join(dir, BeforeRestoreFileName)
	for path, data := range map[string]string{live: "new", kept: "earlier"} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mem, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer mem.Close()
	c, err := mem.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := keepBeforeRestore(ctx, c, live); !errors.Is(err, context.Canceled) {
		t.Errorf("keepBeforeRestore after cancel = %v, want context.Canceled", err)
	}
	if got, err := os.ReadFile(kept); err != nil || string(got) != "earlier" {
		t.Errorf("kept copy = %q (%v), want the earlier one", got, err)
	}
	if err := keepBeforeRestore(t.Context(), c, live); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(kept); err != nil || string(got) != "new" {
		t.Errorf("kept copy = %q (%v), want the database being replaced", got, err)
	}
}

// The WAL index stays in this process's memory: mapped from a -shm file, it is
// a SIGBUS when the library's drive drops.
func TestOpenMapsNoSharedMemory(t *testing.T) {
	live, _ := newBackedUp(t)
	for range 2 { // a new WAL, and one to carry on with
		d, err := New(context.Background(), live, logger.NewNoopLogger())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.SQL.Exec(`INSERT INTO user_labels (label, kind) VALUES (hex(randomblob(4)), 'EVENT')`); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(live + "-shm"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a -shm file is in use: %v", err)
		}
		d.Close()
	}
}

// With no database there, a restore puts the whole backup in place or nothing:
// an empty library beside its backup would have the next copy overwrite it.
func TestRestoreWithoutADatabase(t *testing.T) {
	for _, tt := range []struct {
		name string
		live func(t *testing.T, live string)
	}{
		{"deleted", func(t *testing.T, live string) { os.Remove(live) }},
		{"empty file", func(t *testing.T, live string) {
			if err := os.WriteFile(live, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"not a database", func(t *testing.T, live string) {
			if err := os.WriteFile(live, bytes.Repeat([]byte("x"), 4096), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			live, dest := newBackedUp(t)
			tt.live(t, live)
			before, beforeErr := os.ReadFile(live)
			// a WAL of the database that is gone
			if err := os.WriteFile(live+"-wal", []byte("stale"), 0o644); err != nil {
				t.Fatal(err)
			}
			kept := filepath.Join(filepath.Dir(live), BeforeRestoreFileName)
			if err := os.WriteFile(kept, []byte("earlier"), 0o644); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := Restore(ctx, dest, live); err == nil {
				t.Fatal("a cancelled restore succeeded")
			}
			if after, err := os.ReadFile(live); !bytes.Equal(after, before) || errors.Is(err, fs.ErrNotExist) != errors.Is(beforeErr, fs.ErrNotExist) {
				t.Errorf("a cancelled restore changed the database: %d bytes (%v)", len(after), err)
			}

			if err := Restore(t.Context(), dest, live); err != nil {
				t.Fatal(err)
			}
			if wal, err := os.ReadFile(live + "-wal"); err == nil && string(wal) == "stale" {
				t.Error("the stale WAL is still beside the restored database")
			}
			d, err := New(t.Context(), live, logger.NewNoopLogger())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			var n int
			if err := d.SQL.Get(&n, `SELECT count(*) FROM user_labels`); err != nil || n != 1 {
				t.Errorf("labels after the restore = %d (%v), want the backup's 1", n, err)
			}
			want := "earlier" // nothing was replaced, so nothing is kept over it
			if len(before) > 0 {
				want = string(before)
			}
			if got, err := os.ReadFile(kept); err != nil || string(got) != want {
				t.Errorf("kept copy is %d bytes (%v), want %d", len(got), err, len(want))
			}
		})
	}
}

func TestDamagedDatabaseIsNamed(t *testing.T) {
	live, _ := newBackedUp(t)
	if err := os.WriteFile(live, bytes.Repeat([]byte("x"), 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(t.Context(), live, logger.NewNoopLogger()); !Damaged(err) {
		t.Errorf("opening a file that is no database = %v, want it Damaged", err)
	}
}

// newHashedLibrary is a closed library holding one file known by hash, as a
// release before the newest migration left it.
func newHashedLibrary(t *testing.T, hash string, placed int) (live string) {
	t.Helper()
	ctx := context.Background()
	live = filepath.Join(t.TempDir(), ".wandersort.db")
	d, err := New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.SQL.ExecContext(ctx, `INSERT INTO file_registry (id, file_dir, file_name, file_size, file_modified_at,
		file_extension, discovered_at, last_seen_at, placed) VALUES (1, '2024', 'a.jpg', 1, 't', '.jpg', 'd', 's', ?)`, placed); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL.ExecContext(ctx, `INSERT INTO file_metadata (file_hash, file_id) VALUES (?, 1)`, hash); err != nil {
		t.Fatal(err)
	}
	return live
}

// A BLAKE3 hash equals no MD5, so every photo of such a library would look new
// and be copied again: it is refused, and left as it was for the version that
// made it.
func TestOpenRefusesOldHashesBeforeMigrating(t *testing.T) {
	for _, tt := range []struct {
		name, hash string
		placed     int
		upgrade    bool // a migration is still to run
		refused    bool
	}{
		{"in the library", "blake3:aa", 1, true, true},
		{"scanned, never copied", "blake3:aa", 0, true, true},
		{"nothing to migrate", "blake3:aa", 1, false, true},
		{"MD5", "md5:aa", 1, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			live := newHashedLibrary(t, tt.hash, tt.placed)
			if tt.upgrade {
				forgetMigrationsAfter(t, live, 3)
			}
			before := newestMigration(t, live)
			d, err := New(ctx, live, logger.NewNoopLogger())
			if err == nil {
				d.Close()
			}
			if errors.Is(err, ErrOldHashes) != tt.refused || err != nil && !tt.refused {
				t.Fatalf("open = %v, want refused: %v", err, tt.refused)
			}
			if !tt.refused {
				return
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(live), PreMigrationBackupFileName)); !os.IsNotExist(err) {
				t.Errorf("a refused library was backed up for an upgrade: %v", err)
			}
			if after := newestMigration(t, live); after != before {
				t.Errorf("newest migration = %d, want %d", after, before)
			}
		})
	}
}

// newestMigration is the last migration the closed library at live has run.
func newestMigration(t *testing.T, live string) (version int) {
	t.Helper()
	r, err := OpenReadOnly(live)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.SQL.Get(&version, `SELECT MAX(version) FROM schema_migrations`); err != nil {
		t.Fatal(err)
	}
	return version
}

// forgetMigrationsAfter makes the closed library at live look older than it is.
func forgetMigrationsAfter(t *testing.T, live string, version int) {
	t.Helper()
	d, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`DELETE FROM schema_migrations WHERE version > ?`, version); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRefusesABackupWithOldHashes(t *testing.T) {
	ctx := context.Background()
	live, _ := newBackedUp(t)
	backup := filepath.Join(filepath.Dir(live), "old.zst")
	if err := compressFile(ctx, newHashedLibrary(t, "blake3:aa", 1), backup); err != nil {
		t.Fatal(err)
	}
	// not ErrOldHashes: the library itself is fine, and is told nothing else
	if err := Restore(ctx, backup, live); !errors.Is(err, ErrOldBackup) || errors.Is(err, ErrOldHashes) {
		t.Fatalf("Restore = %v, want ErrOldBackup alone", err)
	}
	d, err := New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatalf("the library no longer opens: %v", err)
	}
	defer d.Close()
	var n int
	if err := d.SQL.Get(&n, `SELECT count(*) FROM user_labels`); err != nil || n != 1 {
		t.Errorf("user_labels = %d, %v; want 1: a refused restore changes nothing", n, err)
	}
}

// A library from an earlier release with files recorded but none read yet has
// no hash to refuse: it opens and is upgraded.
func TestOpenUpgradesALibraryWithNothingHashed(t *testing.T) {
	ctx := context.Background()
	live := newHashedLibrary(t, "md5:aa", 0)
	old, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DELETE FROM file_metadata`,
		`ALTER TABLE file_metadata DROP COLUMN file_sha256`,
		`DELETE FROM schema_migrations WHERE version > 4`,
	} {
		if _, err := old.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()

	d, err := New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatalf("open = %v, want it upgraded", err)
	}
	defer d.Close()
	var files, hashed int
	if err := d.SQL.Get(&files, `SELECT count(*) FROM file_registry`); err != nil || files != 1 {
		t.Errorf("files = %d, %v; want the recorded one", files, err)
	}
	if err := d.SQL.Get(&hashed, `SELECT count(file_sha256) FROM file_metadata`); err != nil || hashed != 0 {
		t.Errorf("hashed = %d, %v; want the new column, empty", hashed, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(live), PreMigrationBackupFileName)); err != nil {
		t.Errorf("no backup before the upgrade: %v", err)
	}
}
