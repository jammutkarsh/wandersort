package db

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/db/migrations"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	_ "modernc.org/sqlite"
)

// DB wraps the standard sql.DB connection.
type DB struct {
	SQL    *sql.DB
	Writer *BulkWriter
	log    logger.Logger
}

// New creates and initializes a new DB instance.
func New(dbPath string, log logger.Logger) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("unable to open database: %w", err)
	}

	appID := appIDFromTag()
	pragmas := []string{
		"PRAGMA page_size=32768",             //  32KB for better I/O efficiency.
		"PRAGMA journal_mode=WAL",            // Better concurrency and durability.
		"PRAGMA synchronous=NORMAL",          // Reduces fsync frequency to improve write performance with acceptable safety.
		"PRAGMA cache_size=-256000",          // ~256MB page cache in memory (negative = size in KB).
		"PRAGMA busy_timeout=5000",           // Wait 5s in database lock before failing.
		"PRAGMA temp_store=MEMORY",           // Stores temporary tables and indices in RAM instead of disk.
		"PRAGMA mmap_size=1073741824",        // 1GB memory-mapped I/O to reduce system calls.
		"PRAGMA foreign_keys=ON",             // Enforces foreign key constraints.
		"PRAGMA auto_vacuum=INCREMENTAL",     // Enables incremental space reclamation.
		"PRAGMA journal_size_limit=67108864", // Limits WAL file size to ~64MB before truncation.
		"PRAGMA wal_autocheckpoint=2000",     // Automatically checkpoints WAL after 2000 pages written.

		fmt.Sprintf("PRAGMA application_id=%d", appID), // Unique identifier for the application.
	}

	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("setting pragma %q: %w", p, err)
		}
	}

	// Single connection: SQLite is single-writer; one connection serializes all
	// access at the Go level and avoids SQLITE_BUSY lock contention entirely.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("unable to ping database: %w", err)
	}

	log.Info("Database connection established", "path", dbPath)

	var count int
	if count, err = migrations.Run(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	log.Info("Migration Completed", "migrations", count)
	log.Info("Successfully connected to sqlite database", "path", dbPath)
	d := &DB{SQL: sqlDB, log: log}
	d.Writer = NewBulkWriter(sqlDB, log)
	return d, nil
}

// Close safely closes the database after running optimization routines.
// Call this instead of db.SQL.Close() during application shutdown.
func (db *DB) Close() error {
	if db.Writer != nil {
		db.Writer.Close()
	}

	// PRAGMA optimize runs an analysis to update query planner statistics.
	// It's highly recommended to run this just before closing the database.
	if _, err := db.SQL.Exec("PRAGMA optimize"); err != nil {
		return fmt.Errorf("pragma optimize failed: %w", err)
	}
	return db.SQL.Close()
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

// InClause builds a placeholder string "?,?,?..." with n question marks and
// returns a slice of interface{} values suitable for use with sql.Query.
// Example: InClause(ids) where ids is []int64 → ("?,?,?", []any{1,2,3})
func InClause[T any](vals []T) (string, []any) {
	args := make([]any, len(vals))
	marks := make([]string, len(vals))
	for i, v := range vals {
		args[i] = v
		marks[i] = "?"
	}
	return strings.Join(marks, ","), args
}

func appIDFromTag() int32 {
	const tag = "WAND"
	return int32(binary.BigEndian.Uint32([]byte(tag)))
}
