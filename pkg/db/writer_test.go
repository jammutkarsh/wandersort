// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// TestFlushDrainsEnqueuedOps guards the Flush contract: every operation
// enqueued before Flush() is called must be visible after it returns.
// The drain loop in start() exists because select order is random — without
// it, a flush request can be served before buffered ops are received.
func TestFlushDrainsEnqueuedOps(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	const n = 500
	for range n {
		if !d.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO user_labels (label, kind) VALUES ('x', 'EVENT')`)
			return err
		}) {
			t.Fatal("writer closed early")
		}
	}
	d.Writer.Flush()

	var count int
	if err := d.SQL.Get(&count, `SELECT COUNT(*) FROM user_labels`); err != nil {
		t.Fatal(err)
	}
	if count != n {
		t.Fatalf("after Flush: %d rows visible, want %d", count, n)
	}
}

func TestWriteSyncReturnsOperationOutcome(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	if err := d.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('sync', 'EVENT')`)
		return err
	}); err != nil {
		t.Fatalf("WriteSync success case: %v", err)
	}
	var count int
	if err := d.SQL.Get(&count, `SELECT COUNT(*) FROM user_labels WHERE label='sync'`); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("WriteSync op not visible after return: count = %d, want 1", count)
	}

	wantErr := fmt.Errorf("deliberate failure")
	if err := d.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("WriteSync error case: got %v, want %v", err, wantErr)
	}
}

func TestWriteReturnsFalseAfterClose(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	d.Writer.Close()

	if d.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error { return nil }) {
		t.Error("Write on a closed writer should return false")
	}
	d.SQL.Close()
}

func TestFlushReturnsImmediatelyWhenClosed(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	d.Writer.Close()

	done := make(chan struct{})
	go func() {
		d.Writer.Flush()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Flush on a closed writer must not block")
	}
	d.SQL.Close()
}

// TestExecuteBatchFallsBackOnOperationFailure pins executeBatch's fallback
// contract: one bad op in a batch must not lose the good ones — they still
// commit individually via executeIndividually, and the reported error names
// exactly how many of the batch failed.
func TestExecuteBatchFallsBackOnOperationFailure(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	good := func(label string) DBOperation {
		return func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES (?, 'EVENT')`, label)
			return err
		}
	}
	bad := func(ctx context.Context, tx *sqlx.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO no_such_table (x) VALUES (1)`)
		return err
	}

	batch := []DBOperation{good("one"), bad, good("two")}
	err = d.Writer.executeBatch(batch)
	if err == nil || !strings.Contains(err.Error(), "1/3") {
		t.Fatalf("executeBatch error = %v, want a 1/3-failed fallback error", err)
	}

	var count int
	if err := d.SQL.Get(&count, `SELECT COUNT(*) FROM user_labels WHERE label IN ('one', 'two')`); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("good ops after fallback: count = %d, want 2", count)
	}
}

func TestExecuteIndividuallyAllSucceed(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), AppDB, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	batch := []DBOperation{
		func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('a', 'EVENT')`)
			return err
		},
		func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('b', 'EVENT')`)
			return err
		},
	}
	if err := d.Writer.executeIndividually(context.Background(), batch); err != nil {
		t.Fatalf("executeIndividually: %v", err)
	}
	var count int
	if err := d.SQL.Get(&count, `SELECT COUNT(*) FROM user_labels`); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}
