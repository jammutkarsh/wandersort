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
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func TestBackupDiffersFromLiveDatabase(t *testing.T) {
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
	// The live file on disk after a checkpoint is the worst case: fully
	// compacted content the backup could otherwise match byte for byte.
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
	if len(liveBytes) == len(bakBytes) {
		t.Errorf("backup and live database are both %d bytes", len(bakBytes))
	}
	if bytes.Equal(liveBytes, bakBytes) {
		t.Error("backup is byte-identical to the live database")
	}

	b, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var n int
	if err := b.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
		t.Errorf("backup not readable: n=%d err=%v", n, err)
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
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = ?`, backupTable).Scan(&stamps); err != nil {
		t.Fatal(err)
	}
	if stamps != 0 {
		t.Error("restored database still carries the backup stamp")
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
	foreign, err := sql.Open("sqlite", dest+".foreign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.Exec(`CREATE TABLE x (y)`); err != nil {
		t.Fatal(err)
	}
	foreign.Close()
	before, _ := os.ReadFile(live)

	if err := Restore(ctx, dest+".foreign", live); err == nil {
		t.Fatal("restoring a non-wandersort database must fail")
	}
	if after, _ := os.ReadFile(live); !bytes.Equal(before, after) {
		t.Error("a refused restore changed the database")
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
	// A directory where the temp copy goes makes VACUUM INTO fail.
	if err := os.Mkdir(dest+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest+".tmp", "x"), nil, 0o644); err != nil {
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
