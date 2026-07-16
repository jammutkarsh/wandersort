package db

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/db/migrations"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

type DBType int

const (
	AppDB DBType = iota
	LocationDB
)

// Pattern: -ING = active phase, -ED = completed/terminal
const (
	StatusStarted    = "STARTED"
	StatusScanning   = "SCANNING"
	StatusScanned    = "SCANNED"
	StatusHashing    = "HASHING"
	StatusHashed     = "HASHED"
	StatusScoring    = "SCORING"
	StatusScored     = "SCORED"
	StatusOrganizing = "ORGANIZING"
	StatusOrganized  = "ORGANIZED"
	StatusAnalyzing  = "ANALYZING"
	StatusAnalyzed   = "ANALYZED"
	StatusCompleted  = "COMPLETED"
	StatusFailed     = "FAILED"
	StatusCancelled  = "CANCELLED"
	StatusDiscovered = "DISCOVERED"
	StatusError      = "ERROR"

	// virtual_fs_entries lifecycle
	StatusProposed = "PROPOSED"
	StatusApproved = "APPROVED"
	StatusDone     = "DONE"
)

// SQLite connection pool and retry tuning
const (
	// maxOpenConns is 1 because SQLite is single-writer — one Go-level connection
	// serialises access and avoids SQLITE_BUSY lock contention
	maxOpenConns = 1
	maxIdleConns = 1
	// connMaxLifetime of 0 means connections live forever, acceptable with maxOpenConns=1
	connMaxLifetime = 0
	// retryInitialBackoff is the starting sleep between SQLITE_BUSY retries
	retryInitialBackoff = 50 * time.Millisecond
	// retryMaxBackoff caps exponential backoff to keep retry latency bounded
	retryMaxBackoff = 500 * time.Millisecond
)

// DB wraps *sql.DB with a BulkWriter for database operations
// BulkWriter is nil for LocationDB connections
type DB struct {
	SQL    *sqlx.DB
	Writer *BulkWriter
	log    logger.Logger
}

// New opens the database at dbPath according to dbType and returns a *DB
func New(ctx context.Context, dbPath string, dbType DBType, log logger.Logger) (*DB, error) {
	switch dbType {
	case AppDB:
		return openAppDB(dbPath, log)
	case LocationDB:
		return openLocationDB(dbPath, log)
	default:
		return nil, fmt.Errorf("unknown DBType %d", dbType)
	}
}

func (d *DB) Close() error {
	if d.Writer != nil {
		d.Writer.Close()

		if _, err := d.SQL.Exec("PRAGMA optimize"); err != nil {
			return fmt.Errorf("pragma optimize: %w", err)
		}
		// WAL mode doesn't fold -wal/-shm back into the main file just because
		// the connection closes — force a full checkpoint so a clean shutdown
		// doesn't leave them behind.
		if _, err := d.SQL.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return fmt.Errorf("wal checkpoint: %w", err)
		}
	}

	return d.SQL.Close()
}

// openAppDB opens the application SQLite database, applies pragma tuning,
// runs migrations, and initialises the BulkWriter for batched writes
func openAppDB(dbPath string, log logger.Logger) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("unable to open database: %w", err)
	}

	appID := appIDFromTag()
	if err := verifyAppID(sqlDB, dbPath, appID); err != nil {
		sqlDB.Close()
		return nil, err
	}

	pragmas := []string{
		"PRAGMA page_size=32768",             //  32KB for better I/O efficiency
		"PRAGMA journal_mode=WAL",            // Better concurrency and durability
		"PRAGMA synchronous=NORMAL",          // Reduces fsync frequency to improve write performance with acceptable safety
		"PRAGMA cache_size=-256000",          // ~256MB page cache in memory (negative = size in KB)
		"PRAGMA busy_timeout=5000",           // Wait 5s in database lock before failing
		"PRAGMA temp_store=MEMORY",           // Stores temporary tables and indices in RAM instead of disk
		"PRAGMA mmap_size=1073741824",        // 1GB memory-mapped I/O to reduce system calls
		"PRAGMA foreign_keys=ON",             // Enforces foreign key constraints
		"PRAGMA auto_vacuum=INCREMENTAL",     // Enables incremental space reclamation
		"PRAGMA journal_size_limit=67108864", // Limits WAL file size to ~64MB before truncation
		"PRAGMA wal_autocheckpoint=2000",     // Automatically checkpoints WAL after 2000 pages written

		fmt.Sprintf("PRAGMA application_id=%d", appID), // Unique identifier for the application
	}

	for _, pragma := range pragmas {
		if _, err := sqlDB.Exec(pragma); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("setting pragma %q: %w", pragma, err)
		}
	}

	// Single connection: SQLite is single-writer; one connection serializes all
	// access at the Go level and avoids SQLITE_BUSY lock contention entirely
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("appDB: unable to ping - %w", err)
	}

	log.Info("Database connection established", "path", dbPath)

	sqlxDB := sqlx.NewDb(sqlDB, "sqlite")

	count, err := migrations.Run(sqlxDB)
	if err != nil {
		sqlxDB.Close()
		return nil, fmt.Errorf("appDB: migrations - %w", err)
	}

	log.Info("Migration completed", "migrations", count)
	log.Info("Successfully connected to sqlite database", "path", dbPath)
	d := &DB{SQL: sqlxDB, log: log}
	d.Writer = NewBulkWriter(sqlxDB, log)
	return d, nil
}

// openLocationDB opens the read-only location database.
func openLocationDB(dbPath string, log logger.Logger) (*DB, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("location database not found at %s: %w", dbPath, err)
	}

	dsn := fmt.Sprintf("file:%s?mode=ro&_journal=OFF&_sync=OFF", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("locationDB: unable to open - %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("locationDB: unable to ping - %w", err)
	}

	log.Info("Successfully connected to location database", "path", dbPath)
	return &DB{SQL: sqlx.NewDb(sqlDB, "sqlite"), log: log}, nil
}

// Optimize reclaims SQLite disk space (incremental_vacuum) and releases
// internal memory (shrink_memory). Call after large delete operations
func (db *DB) Optimize(ctx context.Context) error {
	// reclaim space safely after large delete operations
	if _, err := db.SQL.ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
		return fmt.Errorf("incremental vacuum failed: %w", err)
	}
	// free as much SQLite internal memory as possible
	// Useful after massive batch operations
	if _, err := db.SQL.ExecContext(ctx, "PRAGMA shrink_memory"); err != nil {
		return fmt.Errorf("shrink memory failed: %w", err)
	}
	return nil
}

// BeginTx starts a new transaction
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sqlx.Tx, error) {
	return db.SQL.BeginTxx(ctx, opts)
}

// ExecContext executes a query without returning any rows
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.SQL.ExecContext(ctx, query, args...)
}

// QueryContext executes a query that returns rows
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.SQL.QueryContext(ctx, query, args...)
}

// QueryRowContext executes a query that is expected to return at most one row
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.SQL.QueryRowContext(ctx, query, args...)
}

// ExecRetry executes a query with exponential backoff if the database is busy
// This is useful for multi-threaded SQLite environments
func (db *DB) ExecRetry(ctx context.Context, query string, args ...any) (sql.Result, error) {
	const maxAttempts = 12
	backoff := retryInitialBackoff
	// Max time: 50ms * (2^12 - 1) = ~3.4s total retry time before giving up

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

		// Exponential backoff capped to keep retries bounded
		if backoff < retryMaxBackoff {
			backoff *= 2
		}
	}

	return nil, lastErr
}

// isSQLITEBusy checks if an error is a SQLite busy/locked error
func isSQLITEBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

func appIDFromTag() int32 {
	const tag = "WAND"
	return int32(binary.BigEndian.Uint32([]byte(tag)))
}

// verifyAppID refuses to claim a sqlite file that already belongs to another
// application: a non-empty database whose application_id isn't ours would
// otherwise be silently stamped and migrated. A fresh or empty file passes and
// is stamped by the pragma loop that follows
func verifyAppID(sqlDB *sql.DB, dbPath string, wantID int32) error {
	var gotID int32
	if err := sqlDB.QueryRow("PRAGMA application_id").Scan(&gotID); err != nil {
		return fmt.Errorf("reading application_id: %w", err)
	}
	if gotID == wantID {
		return nil
	}

	var objects int
	if err := sqlDB.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&objects); err != nil {
		return fmt.Errorf("inspecting database schema: %w", err)
	}
	if objects > 0 {
		return fmt.Errorf("%s is not a wandersort database (application_id %d)", dbPath, gotID)
	}
	return nil
}

// IntOrNil parses s as an int, returning nil if s is empty or invalid.
func IntOrNil(s string) any {
	if s == "" {
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return v
}

// FloatOrNil parses s as a float64, returning nil if s is empty or invalid.
func FloatOrNil(s string) any {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return v
}

// StrOrNil returns s, or nil if s is empty.
func StrOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
