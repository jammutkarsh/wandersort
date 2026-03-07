package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// DBOperation represents a single database mutation.
type DBOperation func(ctx context.Context, tx *sql.Tx) error

// BulkWriter batches multiple database operations into single transactions
// to minimize lock contention and improve write performance in SQLite.
type BulkWriter struct {
	sqlDB         *sql.DB
	log           logger.Logger
	ops           chan DBOperation
	batchSize     int
	flushInterval time.Duration
	done          chan struct{}
	closed        atomic.Bool
}

// NewBulkWriter creates a new bulk writer.
func NewBulkWriter(sqlDB *sql.DB, log logger.Logger) *BulkWriter {
	bw := &BulkWriter{
		sqlDB:         sqlDB,
		log:           log,
		ops:           make(chan DBOperation, 10000), // Large buffer to prevent blocking
		batchSize:     5000,                          // Optimal batch size for SQLite
		flushInterval: 100 * time.Millisecond,        // Flush periodically even if batch isn't full
		done:          make(chan struct{}),
	}
	go bw.start()
	return bw
}

// Write enqueues an operation to be executed in the next batch.
// Returns false if the writer has already been closed.
func (bw *BulkWriter) Write(op DBOperation) bool {
	if bw.closed.Load() {
		return false
	}
	bw.ops <- op
	return true
}

// Close gracefully shuts down the bulk writer, flushing any pending operations.
func (bw *BulkWriter) Close() {
	if bw.ops != nil {
		bw.closed.Store(true)
		close(bw.ops)
		<-bw.done
	}
}

func (bw *BulkWriter) start() {
	defer close(bw.done)

	var batch []DBOperation
	ticker := time.NewTicker(bw.flushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := bw.executeBatch(batch); err != nil {
			bw.log.Error("Bulk DB write failed", "error", err, "size", len(batch))
		}
		// Try to reuse the slice capacity
		batch = batch[:0]
	}

	for {
		select {
		case op, ok := <-bw.ops:
			if !ok {
				flush() // Flush remaining on close
				return
			}
			batch = append(batch, op)
			if len(batch) >= bw.batchSize {
				bw.log.Debug("Flushing bulk writer batch", "size", len(batch))
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (bw *BulkWriter) executeBatch(batch []DBOperation) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tx, err := bw.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, op := range batch {
		if err := op(ctx, tx); err != nil {
			return fmt.Errorf("operation failed: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}
