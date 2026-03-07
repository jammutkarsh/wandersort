package db

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		"PRAGMA page_size=32768",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=-256000",
		"PRAGMA busy_timeout=5000",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA mmap_size=1073741824",
		"PRAGMA foreign_keys=ON",
		"PRAGMA auto_vacuum=INCREMENTAL",
		"PRAGMA journal_size_limit=67108864",
		"PRAGMA locking_mode=EXCLUSIVE",
		"PRAGMA wal_autocheckpoint=2000",

		fmt.Sprintf("PRAGMA application_id=%d", appID),
	}

	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("setting pragma %q: %w", p, err)
		}
	}

	// Connection pool: 1 writer + 3 readers (perfect for WAL)
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("unable to ping database: %w", err)
	}

	log.Info("Database connection established", "path", dbPath)

	if err := migrations.Run(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

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
