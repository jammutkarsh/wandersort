package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// DB wraps the standard sql.DB connection.
type DB struct {
	SQL    *sql.DB
	Writer *BulkWriter
	log    logger.Logger
}

// New wraps an already-opened *sql.DB into a *DB and starts the BulkWriter.
func New(sqlDB *sql.DB, log logger.Logger) *DB {
	d := &DB{SQL: sqlDB, log: log}
	d.Writer = NewBulkWriter(sqlDB, log)
	return d
}

func (db *DB) Optimize(ctx context.Context) error {
	// reclaim space safely after large delete operations.
	if _, err := db.SQL.ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
		return fmt.Errorf("incremental vacuum failed: %w", err)
	}
	// free as much SQLite internal memory as possible.
	// Useful after massive batch operations.
	if _, err := db.SQL.ExecContext(ctx, "PRAGMA shrink_memory"); err != nil {
		return fmt.Errorf("shrink memory failed: %w", err)
	}
	return nil
}

// BeginTx starts a new transaction.
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return db.SQL.BeginTx(ctx, opts)
}

// ExecContext executes a query without returning any rows.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.SQL.ExecContext(ctx, query, args...)
}

// QueryContext executes a query that returns rows.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.SQL.QueryContext(ctx, query, args...)
}

// QueryRowContext executes a query that is expected to return at most one row.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.SQL.QueryRowContext(ctx, query, args...)
}

// ExecRetry executes a query with exponential backoff if the database is busy.
// This is useful for multi-threaded SQLite environments.
func (db *DB) ExecRetry(ctx context.Context, query string, args ...any) (sql.Result, error) {
	const maxAttempts = 12
	backoff := 50 * time.Millisecond
	// Max time: 50ms * (2^12 - 1) = ~3.4s total retry time before giving up.

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := db.SQL.ExecContext(ctx, query, args...)
		if err == nil {
			return result, nil
		}
		lastErr = err

		if !isSQLITEBusy(err) || attempt == maxAttempts {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		// Exponential backoff capped to keep retries bounded.
		if backoff < 500*time.Millisecond {
			backoff *= 2
		}
	}

	return nil, lastErr
}

func isSQLITEBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}
