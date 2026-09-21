// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
)

// newAdminTestCmd builds an `admin db` command with the given bool flags set
// to true. flagPlain is always registered, and set, so a confirm reads stdin
// instead of drawing a TUI.
func newAdminTestCmd(t *testing.T, set ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "db"}
	for _, f := range []string{flagYes, flagPlain, flagRestore, flagReset} {
		cmd.Flags().Bool(f, false, "")
	}
	for _, f := range append(set, flagPlain) {
		if err := cmd.Flags().Set(f, "true"); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

// answerStdin makes the next plain confirm prompt read answer.
func answerStdin(t *testing.T, answer string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(answer); err != nil {
		t.Fatal(err)
	}
	w.Close()
	realStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = realStdin })
}

func TestRunResetWithoutDBFlagKeepsDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".wandersort.db")
	seedDB(t, dbPath)
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// No stdin answer and no --yes: a prompt here would read EOF and cancel.
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{AppDBPath: dbPath}}
	if err := a.runAdminClear(); err != nil {
		t.Fatalf("admin db: %v", err)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("reset without --db changed the database")
	}
	if _, err := os.Stat(filepath.Join(dir, db.BackupFileName)); !os.IsNotExist(err) {
		t.Error("reset without --db wrote a backup")
	}
}

func TestRunResetNoDatabase(t *testing.T) {
	dir := t.TempDir()
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		AppDBPath: filepath.Join(dir, ".wandersort.db"),
	}}
	if err := a.runAdminDB(newAdminTestCmd(t, flagReset, flagYes)); err == nil {
		t.Fatal("admin db --reset with no database on disk must fail")
	}
}

func TestRunResetCancelledWithoutYes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".wandersort.db")
	seedDB(t, dbPath)

	answerStdin(t, "n\n") // the plain confirm prompt's decline path

	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		AppDBPath: dbPath,
	}}
	if err := a.runAdminDB(newAdminTestCmd(t, flagReset)); err == nil {
		t.Fatal("declining the confirm prompt must cancel the reset")
	}
}

func TestRunResetYesWipesDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".wandersort.db")
	seedDB(t, dbPath)

	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{
		AppDBPath: dbPath,
	}}
	if err := a.runAdminDB(newAdminTestCmd(t, flagReset, flagYes)); err != nil {
		t.Fatalf("admin db: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, db.BackupFileName)); err != nil {
		t.Errorf("reset --db left no backup to recover from: %v", err)
	}
}

// TestRecoverUndoesReset is the reason reset --db backs up first — and a
// second reset over the already-empty database must not replace that backup.
func TestRecoverUndoesReset(t *testing.T) {
	for _, resets := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d resets", resets), func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), ".wandersort.db")
			seedDB(t, dbPath)

			a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{AppDBPath: dbPath}}
			for range resets {
				if err := a.runAdminDB(newAdminTestCmd(t, flagReset, flagYes)); err != nil {
					t.Fatalf("admin db: %v", err)
				}
				a.AppDB = nil // closeDBs closes but doesn't clear it
			}
			if err := a.runAdminDB(newAdminTestCmd(t, flagRestore, flagYes)); err != nil {
				t.Fatalf("admin db --restore: %v", err)
			}

			d, err := db.New(ctx, dbPath, db.AppDB, logger.NewNoopLogger())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			var n int
			if err := d.QueryRowContext(ctx, `SELECT count(*) FROM user_labels`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Errorf("user_labels = %d rows after recover, want 1", n)
			}
		})
	}
}

func TestRecoverWithoutBackupFails(t *testing.T) {
	dir := t.TempDir()
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{AppDBPath: filepath.Join(dir, ".wandersort.db")}}
	if err := a.runAdminDB(newAdminTestCmd(t, flagRestore, flagYes)); err == nil {
		t.Fatal("recover with no backup must fail")
	}
}

func TestRecoverCancelledWithoutYes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".wandersort.db")
	if err := os.WriteFile(filepath.Join(dir, db.BackupFileName), []byte("bak"), 0o644); err != nil {
		t.Fatal(err)
	}
	answerStdin(t, "n\n")
	a := &app{Log: logger.NewNoopLogger(), Config: &config.Configuration{AppDBPath: dbPath}}
	if err := a.runAdminDB(newAdminTestCmd(t, flagRestore)); err == nil {
		t.Fatal("declining the prompt must cancel the recover")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Error("a cancelled recover wrote the database")
	}
}

// seedDB creates a real app database at dbPath holding one label, so reset
// --db finds something to wipe.
func seedDB(t *testing.T, dbPath string) {
	t.Helper()
	d, err := db.New(context.Background(), dbPath, db.AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(context.Background(), `INSERT INTO user_labels (label, kind) VALUES ('Goa', 'EVENT')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}
