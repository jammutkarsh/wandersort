// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	writerBufferSize = 10000
	// writerBatchSize triggers a flush when the pending batch reaches this many ops
	writerBatchSize = 5000
	// writerFlushInterval ensures periodic flushes even if batch size isn't reached
	writerFlushInterval = 100 * time.Millisecond
)

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

// WriteSync runs op in a transaction of its own, after everything already
// enqueued, and returns the transaction's outcome: nil means committed. Use
// it for writes whose outcome must be reported (a review save, a file
// recorded as placed), as opposed to pipeline writes where Write's
// fire-and-forget batching is the point.
//
// It does not go through the batch. A batch that fails replays its ops one
// by one, so an op run inside it could report success from a transaction
// that was then rolled back — and whether the replay happens at all depends
// on every other op in the batch. There is one connection, so a transaction
// of its own costs no contention; holding the read lock keeps Close from
// shutting the database under it.
func (bw *BulkWriter) WriteSync(op DBOperation) error {
	bw.Flush()
	bw.mu.RLock()
	defer bw.mu.RUnlock()
	if bw.closed.Load() {
		return fmt.Errorf("writer closed")
	}
	ctx := context.Background()
	tx, err := bw.sqlDB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if err := op(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// DryRun runs op in a transaction of its own and always rolls it back: a
// read of what the database would hold after op, with nothing written. Same
// ordering and lock as WriteSync.
func (bw *BulkWriter) DryRun(op DBOperation) error {
	bw.Flush()
	bw.mu.RLock()
	defer bw.mu.RUnlock()
	if bw.closed.Load() {
		return fmt.Errorf("writer closed")
	}
	ctx := context.Background()
	tx, err := bw.sqlDB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	return op(ctx, tx)
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
		// flushReqs is buffered, so the send lands even when the loop has
		// already exited on a concurrent Close; done is what says so then.
		select {
		case <-req.done:
		case <-bw.done:
		}
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

// executeBatch runs all operations in a single transaction, falling back to
// executeIndividually on any failure so one bad op costs only itself.
//
// The fallback re-invokes every op, including ones that already ran in the
// rolled-back batch, so an op must have no effect beyond its transaction —
// anything it does outside tx happens once per attempt. Ops that need a
// reported outcome go through WriteSync, which never replays.
//
// No deadline: an op here is a durable write already accepted from a phase,
// and aborting it partway only turns a slow disk into lost rows. SQLite's own
// busy_timeout bounds lock waits, and the one connection has nothing else to
// wait on.
func (bw *BulkWriter) executeBatch(batch []DBOperation) error {
	ctx := context.Background()

	tx, err := bw.sqlDB.BeginTxx(ctx, nil)
	if err != nil {
		bw.log.Warn("Bulk batch begin failed; retrying operations individually", "error", err, "size", len(batch))
		return bw.executeIndividually(ctx, batch)
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
