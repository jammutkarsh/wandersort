// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/db/migrations"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	_ "modernc.org/sqlite"
)

type DBType int

const (
	AppDB DBType = iota
	LocationDB
)

const (
	// locationDownloadBaseURL is the download URL for the locationDB asset
	// Upstream update schedules and data details can be found at the source URL
	locationDownloadBaseURL = "https://locationdb.utkarshchourasia.in"

	LocationDBFileName = "location.db"

	// locationMetaFileName is the metadata JSON published alongside the LocationDB
	// location.json holds the dynamic metadata (version, date) used to determine if a re-download is required
	LocationMetaFileName = "location.json"
)

// DB wraps *sql.DB with a BulkWriter for database operations
// BulkWriter is nil for LocationDB connections
type DB struct {
	SQL    *sql.DB
	Writer *BulkWriter
	log    logger.Logger
}

// New opens the database at dbPath according to dbType and returns a *DB
func New(dbPath string, dbType DBType, log logger.Logger) (*DB, error) {
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
	}

	if d.Writer != nil {
		if _, err := d.SQL.Exec("PRAGMA optimize"); err != nil {
			return fmt.Errorf("pragma optimize: %w", err)
		}
	}

	return d.SQL.Close()
}

func openAppDB(dbPath string, log logger.Logger) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("unable to open database: %w", err)
	}

	appID := appIDFromTag()
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

	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("setting pragma %q: %w", p, err)
		}
	}

	// Single connection: SQLite is single-writer; one connection serializes all
	// access at the Go level and avoids SQLITE_BUSY lock contention entirely
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("appDB: unable to ping - %w", err)
	}

	log.Info("Database connection established", "path", dbPath)

	count, err := migrations.Run(sqlDB)
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("appDB: migrations - %w", err)
	}

	log.Info("Migration completed", "migrations", count)
	log.Info("Successfully connected to sqlite database", "path", dbPath)

	d := &DB{SQL: sqlDB, log: log}
	d.Writer = NewBulkWriter(sqlDB, log)
	return d, nil
}

func openLocationDB(dbPath string, log logger.Logger) (*DB, error) {
	if err := ensureLocationDB(dbPath, log); err != nil {
		return nil, fmt.Errorf("ensure location db: %w", err)
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
	return &DB{SQL: sqlDB, log: log}, nil
}

// ensureLocationDB creates the parent directory and downloads location.db if the file does not already exist at dbPath
func ensureLocationDB(dbPath string, log logger.Logger) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return fmt.Errorf("create dir %q: %w", dbPath, err)
	}

	if _, err := os.Stat(dbPath); err == nil {
		log.Info("location db found", "path", dbPath)
		return nil
	}

	log.Info("location db not found; downloading", "url", locationDownloadBaseURL+"/"+LocationDBFileName)

	if err := DownloadFile(dbPath, locationDownloadBaseURL+"/"+LocationDBFileName); err != nil {
		return fmt.Errorf("download %s: %w", LocationDBFileName, err)
	}

	metaPath := filepath.Join(filepath.Dir(dbPath), LocationMetaFileName)
	if err := DownloadFile(metaPath, locationDownloadBaseURL+"/"+LocationMetaFileName); err != nil {
		log.Info("location db: could not download metadata (non-fatal)", "file", LocationMetaFileName, "error", err)
	}

	return nil
}

// DownloadFile fetches url and writes the body to dest atomically (via a temp
// file) so a partial download never leaves a corrupt file at dest
func DownloadFile(dest, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	// Write to a temp file in the same directory so os.Rename is atomic
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dl-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op if Rename succeeded
	}()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("rename to %s: %w", dest, err)
	}

	return nil
}

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
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return db.SQL.BeginTx(ctx, opts)
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
	backoff := 50 * time.Millisecond
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

func appIDFromTag() int32 {
	const tag = "WAND"
	return int32(binary.BigEndian.Uint32([]byte(tag)))
}
