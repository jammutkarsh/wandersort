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
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	const n = 500
	for i := range n {
		label := fmt.Sprintf("x%d", i) // labels are a set: each op a new one
		if !d.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO user_labels (label, kind) VALUES (?, 'EVENT')`, label)
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

// DryRun lets op see its own writes and then keeps none of them; op's error
// comes back as is.
func TestDryRunRollsBack(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	count := func(q sqlx.QueryerContext) int {
		var n int
		if err := sqlx.GetContext(context.Background(), q, &n, `SELECT COUNT(*) FROM user_labels WHERE label='dry'`); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := d.Writer.DryRun(func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('dry', 'EVENT')`); err != nil {
			return err
		}
		if n := count(tx); n != 1 {
			t.Errorf("inside the dry run: count = %d, want its own write visible", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := count(d.SQL); n != 0 {
		t.Errorf("after the dry run: count = %d, want nothing kept", n)
	}
	wantErr := fmt.Errorf("deliberate failure")
	if err := d.Writer.DryRun(func(context.Context, *sqlx.Tx) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Errorf("DryRun error = %v, want %v", err, wantErr)
	}
	d.Writer.Close()
	if err := d.Writer.DryRun(func(context.Context, *sqlx.Tx) error { return nil }); err == nil {
		t.Error("DryRun on a closed writer must fail")
	}
}

func TestWriteSyncReturnsOperationOutcome(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), logger.NewNoopLogger())
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

// TestWriteSyncTruthfulBesideFailingBatch guards WriteSync's one promise: nil
// means committed. It used to run inside the batch, so an op that succeeded in
// a batch another op then failed reported nil from a rolled-back transaction —
// the review save and the placed-file record both trusted that nil.
func TestWriteSyncTruthfulBesideFailingBatch(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	insert := func(label string) DBOperation {
		return func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES (?, 'EVENT')`, label)
			return err
		}
	}
	d.Writer.Write(insert("before"))
	d.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error { return errors.New("poison") })

	// sees what was enqueued before it
	var before int
	if err := d.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		return tx.GetContext(ctx, &before, `SELECT COUNT(*) FROM user_labels WHERE label = 'before'`)
	}); err != nil || before != 1 {
		t.Fatalf("WriteSync ran before earlier writes: err=%v before=%d", err, before)
	}

	if err := d.Writer.WriteSync(insert("sync")); err != nil {
		t.Fatalf("WriteSync: %v", err)
	}
	// an op that writes and then fails leaves nothing behind
	failing := func(ctx context.Context, tx *sqlx.Tx) error {
		if err := insert("rolled-back")(ctx, tx); err != nil {
			return err
		}
		return errors.New("late failure")
	}
	if err := d.Writer.WriteSync(failing); err == nil {
		t.Fatal("WriteSync reported success for a failing op")
	}
	var got []string
	if err := d.SQL.Select(&got, `SELECT label FROM user_labels ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "before,sync" {
		t.Fatalf("labels = %v, want [before sync]", got)
	}
}

func TestWriteReturnsFalseAfterClose(t *testing.T) {
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), logger.NewNoopLogger())
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
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), logger.NewNoopLogger())
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
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), logger.NewNoopLogger())
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
	d, err := New(context.Background(), filepath.Join(t.TempDir(), "test.db"), logger.NewNoopLogger())
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

// TestFlushReturnsOnceTheLoopHasExited guards Flush against a Close racing
// it: flushReqs is buffered, so the request lands even after the drain loop
// is gone, and nothing would ever answer it. Stated directly — a loop that
// has exited (done closed) while closed has not been read yet.
func TestFlushReturnsOnceTheLoopHasExited(t *testing.T) {
	bw := &BulkWriter{flushReqs: make(chan flushReq, 1), done: make(chan struct{})}
	close(bw.done)
	returned := make(chan struct{})
	go func() {
		bw.Flush()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Flush blocked forever on a writer whose loop had exited")
	}
}
