package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// DBOperation represents a single database mutation.
type DBOperation func(ctx context.Context, tx *sql.Tx) error

// flushReq is sent by Flush() to signal the background goroutine to drain
// all pending operations and report back when done.
type flushReq struct {
	done chan struct{}
}

// BulkWriter batches multiple database operations into single transactions
// to minimize lock contention and improve write performance in SQLite.
type BulkWriter struct {
	sqlDB         *sql.DB
	log           logger.Logger
	ops           chan DBOperation
	flushReqs     chan flushReq
	batchSize     int
	flushInterval time.Duration
	done          chan struct{}
	mu            sync.RWMutex
	closed        atomic.Bool
}

// NewBulkWriter creates a new bulk writer.
func NewBulkWriter(sqlDB *sql.DB, log logger.Logger) *BulkWriter {
	bw := &BulkWriter{
		sqlDB:         sqlDB,
		log:           log,
		ops:           make(chan DBOperation, 10000), // Large buffer to prevent blocking
		flushReqs:     make(chan flushReq, 1),
		batchSize:     5000,                   // Optimal batch size for SQLite
		flushInterval: 100 * time.Millisecond, // Flush periodically even if batch isn't full
		done:          make(chan struct{}),
	}
	go bw.start()
	return bw
}

// Write enqueues an operation to be executed in the next batch.
// Returns false if the writer has already been closed.
func (bw *BulkWriter) Write(op DBOperation) bool {
	bw.mu.RLock()
	defer bw.mu.RUnlock()

	if bw.closed.Load() {
		return false
	}

	bw.ops <- op
	return true
}

// Flush blocks until all currently-enqueued operations have been written to the
// database. Use this at phase boundaries to guarantee visibility before reads.
func (bw *BulkWriter) Flush() {
	if bw.closed.Load() {
		return
	}
	req := flushReq{done: make(chan struct{})}
	bw.flushReqs <- req
	<-req.done
}

// Close gracefully shuts down the bulk writer, flushing any pending operations.
func (bw *BulkWriter) Close() {
	bw.mu.Lock()
	if bw.closed.Load() {
		bw.mu.Unlock()
		return
	}
	bw.closed.Store(true)
	close(bw.ops)
	bw.mu.Unlock()
	<-bw.done
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
		case req := <-bw.flushReqs:
			flush()
			close(req.done)
		case <-ticker.C:
			flush()
		}
	}
}

func (bw *BulkWriter) executeBatch(batch []DBOperation) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := bw.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, op := range batch {
		if err := op(ctx, tx); err != nil {
			_ = tx.Rollback()
			bw.log.Warn("Bulk batch operation failed; retrying operations individually", "error", err, "size", len(batch))
			return bw.executeIndividually(ctx, batch)
		}
	}

	if err := tx.Commit(); err != nil {
		bw.log.Warn("Bulk batch commit failed; retrying operations individually", "error", err, "size", len(batch))
		return bw.executeIndividually(ctx, batch)
	}

	return nil
}

func (bw *BulkWriter) executeIndividually(ctx context.Context, batch []DBOperation) error {
	var failed int

	for i, op := range batch {
		tx, err := bw.sqlDB.BeginTx(ctx, nil)
		if err != nil {
			failed++
			bw.log.Error("Bulk writer fallback begin tx failed", "index", i, "error", err)
			continue
		}

		if err := op(ctx, tx); err != nil {
			_ = tx.Rollback()
			failed++
			bw.log.Error("Bulk writer fallback operation failed", "index", i, "error", err)
			continue
		}

		if err := tx.Commit(); err != nil {
			failed++
			bw.log.Error("Bulk writer fallback commit failed", "index", i, "error", err)
			continue
		}
	}

	if failed > 0 {
		return fmt.Errorf("bulk writer fallback failed for %d/%d operations", failed, len(batch))
	}
	return nil
}
