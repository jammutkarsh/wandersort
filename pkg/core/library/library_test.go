package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Restore brings the backed-up rows back and drops the draft, whose edits
// were made against the plan the restore replaced.
func TestRestoreDropsTheDraft(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	live := filepath.Join(dir, ".wandersort.db")
	d, err := db.New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, d, 1, "/src", "a.jpg", 1)
	if err := d.Backup(ctx, filepath.Join(dir, db.BackupFileName)); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(dir, vfs.DraftFileName)
	if err := os.WriteFile(draft, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Restore(ctx, live, logger.NewNoopLogger()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(draft); !os.IsNotExist(err) {
		t.Errorf("draft still there after a restore: %v", err)
	}
	d, err = db.New(ctx, live, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	if err := d.SQL.Get(&n, `SELECT count(*) FROM file_registry`); err != nil || n != 1 {
		t.Errorf("restored rows = %d, %v; want 1", n, err)
	}
}

// Resetting an empty library writes nothing, so the backup holding an earlier
// reset's data survives.
func TestResetOfAnEmptyLibraryKeepsTheBackup(t *testing.T) {
	d := dbtest.New(t)
	dir := t.TempDir()
	err := Reset(context.Background(), d, dir, logger.NewNoopLogger())
	if !errors.Is(err, ErrNothingToReset) {
		t.Fatalf("Reset = %v, want ErrNothingToReset", err)
	}
	if _, err := os.Stat(filepath.Join(dir, db.BackupFileName)); !os.IsNotExist(err) {
		t.Errorf("an empty reset wrote a backup: %v", err)
	}
}
