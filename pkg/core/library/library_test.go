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

// Discard forgets the files not yet copied and the folders only they used;
// a placed file, its folder and its record stay.
func TestDiscardKeepsPlacedFiles(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	dir := t.TempDir()
	dbtest.SeedFile(t, d, 1, "/src", "placed.jpg", 1)
	dbtest.SeedEntry(t, d, 1, "/src/placed.jpg", "2024/06_June/placed.jpg")
	dbtest.SeedPlaced(t, d, 1)
	dbtest.SeedFile(t, d, 2, "/src", "same-month.jpg", 1)
	dbtest.SeedEntry(t, d, 2, "/src/same-month.jpg", "2024/06_June/same-month.jpg")
	dbtest.SeedFile(t, d, 3, "/src", "planned.jpg", 1)
	dbtest.SeedEntry(t, d, 3, "/src/planned.jpg", "2025/01_January/planned.jpg")
	draft := filepath.Join(dir, vfs.DraftFileName)
	if err := os.WriteFile(draft, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := Discard(ctx, d, dir)
	if err != nil || n != 2 {
		t.Fatalf("Discard = %d, %v; want 2 files forgotten", n, err)
	}
	var files []int64
	if err := d.SQL.Select(&files, `SELECT id FROM file_registry`); err != nil || len(files) != 1 || files[0] != 1 {
		t.Errorf("files left = %v, %v; want only the placed one", files, err)
	}
	var folders []string
	if err := d.SQL.Select(&folders, `SELECT name FROM folder_nodes ORDER BY name`); err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 || folders[0] != "06_June" || folders[1] != "2024" {
		t.Errorf("folders left = %v, want the placed file's chain only", folders)
	}
	if _, err := os.Stat(draft); !os.IsNotExist(err) {
		t.Errorf("draft still there after a discard: %v", err)
	}
}
