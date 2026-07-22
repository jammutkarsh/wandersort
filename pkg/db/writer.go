package db

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

const (
	// writerBufferSize is the channel buffer for incoming write operations
	writerBufferSize = 10000
	// writerBatchSize triggers a flush when the pending batch reaches this many ops
	writerBatchSize = 5000
	// writerFlushInterval ensures periodic flushes even if batch size isn't reached
	writerFlushInterval = 100 * time.Millisecond
	// batchExecutionTimeout is the context deadline for executing a single batch
	batchExecutionTimeout = 5 * time.Second
)

// DBOperation represents a single database mutation
type DBOperation func(ctx context.Context, tx *sqlx.Tx) error

// flushReq is sent by Flush() to signal the background goroutine to drain
// all pending operations and report back when done
type flushReq struct {
	done chan struct{}
}

// BulkWriter batches multiple database operations into single transactions
// to minimize lock contention and improve write performance in SQLite
type BulkWriter struct {
	sqlDB         *sqlx.DB
	log           logger.Logger
	ops           chan DBOperation
	flushReqs     chan flushReq
	batchSize     int
	flushInterval time.Duration
	done          chan struct{}
	mu            sync.RWMutex
	closed        atomic.Bool
}

// NewBulkWriter creates a new bulk writer
func NewBulkWriter(sqlDB *sqlx.DB, log logger.Logger) *BulkWriter {
	bw := &BulkWriter{
		sqlDB:         sqlDB,
		log:           log,
		ops:           make(chan DBOperation, writerBufferSize),
		flushReqs:     make(chan flushReq, 1),
		batchSize:     writerBatchSize,
		flushInterval: writerFlushInterval,
		done:          make(chan struct{}),
	}
	go bw.start()
	return bw
}

// Write enqueues an operation to be executed in the next batch
// Returns false if the writer has already been closed
func (bw *BulkWriter) Write(op DBOperation) bool {
	bw.mu.RLock()
	defer bw.mu.RUnlock()

	if bw.closed.Load() {
		return false
	}

	bw.ops <- op
	return true
}

// WriteSync enqueues op, blocks until it has actually been executed, and
// returns the op's error. Use it for user-initiated writes whose outcome must
// be reported (a review confirm), as opposed to pipeline writes where Write's
// fire-and-forget batching is the point.
func (bw *BulkWriter) WriteSync(op DBOperation) error {
	// buffered for both attempts: the batch tx, then the individual-tx
	// fallback the batch failure path replays every op through
	res := make(chan error, 2)
	wrapped := func(ctx context.Context, tx *sqlx.Tx) error {
		err := op(ctx, tx)
		res <- err
		return err
	}
	if !bw.Write(wrapped) {
		return fmt.Errorf("writer closed")
	}
	bw.Flush()
	// Flush returning means every enqueued op (including a fallback replay)
	// has run, so the last result is the authoritative one
	var err error
	got := false
	for {
		select {
		case e := <-res:
			err, got = e, true
		default:
			if !got {
				return fmt.Errorf("writer closed before write executed")
			}
			return err
		}
	}
}

// Flush blocks until all currently-enqueued operations have been written to the
// database. Use this at phase boundaries to guarantee visibility before reads
func (bw *BulkWriter) Flush() {
	if bw.closed.Load() {
		return
	}
	req := flushReq{done: make(chan struct{})}
	select {
	case bw.flushReqs <- req:
		<-req.done
	case <-bw.done:
		// Writer is closed or shutting down, abandon flush request
		return
	}
}

// Close gracefully shuts down the bulk writer, flushing any pending operations
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

// start is the background drain loop that batches incoming write operations
// and flushes them to SQLite on batch-size thresholds or timer ticks
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
		// Clear pointers to allow GC of captured variables in DBOperation closures
		for i := range batch {
			batch[i] = nil
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
			// Drain everything already enqueued before reporting the flush
			// done: ops sent before Flush() was called are guaranteed to be
			// in the channel buffer, but select order is random, so this
			// request may have been picked before those ops were received
		drain:
			for {
				select {
				case op, ok := <-bw.ops:
					if !ok {
						break drain // closed; main loop handles shutdown
					}
					batch = append(batch, op)
				default:
					break drain
				}
			}
			flush()
			close(req.done)
		case <-ticker.C:
			flush()
		}
	}
}

// executeBatch runs all operations in a single transaction
// Falls back to executeIndividually on commit/operation failure
func (bw *BulkWriter) executeBatch(batch []DBOperation) error {
	ctx, cancel := context.WithTimeout(context.Background(), batchExecutionTimeout)
	defer cancel()

	tx, err := bw.sqlDB.BeginTxx(ctx, nil)
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

// executeIndividually runs each operation in its own transaction as a fallback
// when the batch transaction fails (e.g. due to SQLITE_BUSY or constraint errors)
func (bw *BulkWriter) executeIndividually(ctx context.Context, batch []DBOperation) error {
	var failed int

	for i, op := range batch {
		tx, err := bw.sqlDB.BeginTxx(ctx, nil)
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
