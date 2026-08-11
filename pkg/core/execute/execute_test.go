// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package execute

import (
	"context"
	"os"
	"path/filepath"
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
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO virtual_fs_entries (file_id, source_path, target_path, status)
		VALUES (?, ?, ?, ?)`, id, src, targetRel, db.StatusApproved); err != nil {
		t.Fatal(err)
	}
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
}

func TestRunRecordsErrorAndContinuesPastFailure(t *testing.T) {
	d := dbtest.New(t)
	out := t.TempDir()
	// row 1's source doesn't exist on disk; row 2 is real and must still run.
	dbtest.SeedFile(t, d, 1, "/no/such/dir", "missing.jpg", 0)
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO virtual_fs_entries (file_id, source_path, target_path, status)
		VALUES (1, '/no/such/dir/missing.jpg', 'missing.jpg', ?)`, db.StatusApproved); err != nil {
		t.Fatal(err)
	}
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
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO virtual_fs_entries (file_id, source_path, target_path, status)
		VALUES (1, ?, 'still-proposed.jpg', ?)`, src, db.StatusProposed); err != nil {
		t.Fatal(err)
	}

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
