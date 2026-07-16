package db

import (
	"context"
	"path/filepath"
	"testing"

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
