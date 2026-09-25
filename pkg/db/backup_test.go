// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
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
	d, err := New(ctx, live, AppDB, logger.NewNoopLogger())
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
	if err := decompressFile(dest, plain); err != nil {
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
	d, err := New(ctx, live, AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('Goa', 'EVENT')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `DELETE FROM user_labels`); err != nil {
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

	d, err = New(ctx, live, AppDB, logger.NewNoopLogger())
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
	d, err := New(ctx, live, AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('Goa', 'EVENT')`); err != nil {
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
	if err := compressFile(plain, dest+".foreign"); err != nil {
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
	d, err := New(ctx, live, AppDB, logger.NewNoopLogger())
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
	d, err := New(ctx, live, AppDB, logger.NewNoopLogger())
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
	open := func() (*DB, error) { return New(ctx, live, AppDB, logger.NewNoopLogger()) }

	d, err := open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, PreMigrationBackupFileName)); !os.IsNotExist(err) {
		t.Fatalf("a fresh database was backed up: %v", err)
	}
	// pretend the newest migration has not run yet
	if _, err := d.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = (SELECT MAX(version) FROM schema_migrations)`); err != nil {
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
	if _, err := d.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (999)`); err != nil {
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
	d, err := New(context.Background(), filepath.Join(t.TempDir(), ".wandersort.db"), AppDB, logger.NewNoopLogger())
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
