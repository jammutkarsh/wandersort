// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

func recordError(t *testing.T, d *db.DB, stage string, err error) {
	t.Helper()
	if !d.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
		return db.RecordError(ctx, tx, 1, stage, "copy", err)
	}) {
		t.Fatal("writer closed")
	}
	d.Writer.Flush()
}

func TestRecordErrorKinds(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"permission", fmt.Errorf("open: %w", fs.ErrPermission), db.KindPermissionDenied},
		{"missing", &fs.PathError{Op: "open", Path: "/x", Err: fs.ErrNotExist}, db.KindNotFound},
		{"disk full", fmt.Errorf("write: %w", syscall.ENOSPC), db.KindNoSpace},
		{"io", fmt.Errorf("read: %w", syscall.EIO), db.KindIO},
		{"checksum", fmt.Errorf("copy: %w", db.ErrChecksumMismatch), db.KindChecksumMismatch},
		{"other", errors.New("boom"), db.KindOther},
		{"panic", db.PanicError("index out of range"), db.KindPanic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := dbtest.New(t)
			dbtest.SeedFile(t, d, 1, "/src", "a.jpg", 1)
			recordError(t, d, db.StageRead, db.WithStack(tt.err))
			var kind string
			if err := d.SQL.Get(&kind, `SELECT kind FROM errors WHERE file_id = 1`); err != nil {
				t.Fatal(err)
			}
			if kind != tt.want {
				t.Errorf("kind = %q, want %q", kind, tt.want)
			}
		})
	}
}

// One row per (file, stage): a second failure replaces the first, counts the
// attempt and keeps when it was first seen; another stage is its own row.
func TestRecordErrorReplacesPerStage(t *testing.T) {
	d := dbtest.New(t)
	dbtest.SeedFile(t, d, 1, "/src", "a.jpg", 1)

	recordError(t, d, db.StageRead, errors.New("first"))
	var first string
	if err := d.SQL.Get(&first, `SELECT first_seen_at FROM errors WHERE file_id = 1`); err != nil {
		t.Fatal(err)
	}
	recordError(t, d, db.StageRead, fs.ErrPermission)
	recordError(t, d, db.StageTransfer, errors.New("other stage"))

	var rows []struct {
		Stage    string `db:"stage"`
		Attempts int    `db:"attempts"`
		Kind     string `db:"kind"`
		First    string `db:"first_seen_at"`
	}
	if err := d.SQL.Select(&rows, `SELECT stage, attempts, kind, first_seen_at FROM errors ORDER BY stage`); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Stage != db.StageRead || rows[0].Attempts != 2 ||
		rows[0].Kind != db.KindPermissionDenied || rows[0].First != first || rows[1].Attempts != 1 {
		t.Errorf("rows = %+v, want READ replaced (2 attempts, first_seen kept) and TRANSFER apart", rows)
	}
}

// The detail is the object the ticket names: message, the chain walked to the
// bottom, frames from where the failure was seen, the errno when there is one.
func TestRecordErrorDetail(t *testing.T) {
	d := dbtest.New(t)
	dbtest.SeedFile(t, d, 1, "/src", "a.jpg", 1)

	err := fmt.Errorf("hash /src/a.jpg: %w", &fs.PathError{Op: "open", Path: "/src/a.jpg", Err: syscall.EACCES})
	recordError(t, d, db.StageRead, db.WithStack(err))

	var raw string
	if e := d.SQL.Get(&raw, `SELECT detail FROM errors WHERE file_id = 1`); e != nil {
		t.Fatal(e)
	}
	var detail db.ErrorDetail
	if e := json.Unmarshal([]byte(raw), &detail); e != nil {
		t.Fatal(e)
	}
	if detail.Message != err.Error() {
		t.Errorf("message = %q", detail.Message)
	}
	if len(detail.Chain) != 3 || detail.Chain[0].Layer != 0 || detail.Chain[2].Error != "permission denied" {
		t.Errorf("chain = %+v, want three layers ending at the errno", detail.Chain)
	}
	if detail.Syscall == nil || detail.Syscall.Errno != "EACCES" || detail.Syscall.Op != "open" {
		t.Errorf("syscall = %+v, want EACCES from open", detail.Syscall)
	}
	if len(detail.Frames) == 0 || !strings.Contains(detail.Frames[0].Func, "TestRecordErrorDetail") {
		t.Errorf("frames = %+v, want the recording call site first", detail.Frames)
	}
}

// A panic's frames are the panic's own stack, not the recovering function's.
func TestPanicErrorKeepsTheRealStack(t *testing.T) {
	var err error
	func() {
		defer func() { err = db.PanicError(recover()) }()
		var m map[string]int
		//lint:ignore SA5000 the panic is the point
		m["x"] = 1 // write to a nil map
	}()
	d := dbtest.New(t)
	dbtest.SeedFile(t, d, 1, "/src", "a.jpg", 1)
	recordError(t, d, db.StageRead, err)

	var raw string
	if e := d.SQL.Get(&raw, `SELECT detail FROM errors WHERE file_id = 1`); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(raw, "TestPanicErrorKeepsTheRealStack") || !strings.Contains(raw, "assignment to entry in nil map") {
		t.Errorf("detail = %s, want the panic message and the function that panicked", raw)
	}
}
