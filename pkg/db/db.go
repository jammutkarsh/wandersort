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
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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

// TimeLayout is RFC3339 with fixed-width nanoseconds. Fixed width keeps
// lexicographic string comparison in SQL consistent with time order; values
// are always stored in UTC via FormatTime and shown in the user's local zone
// only at display time
const TimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// FormatTime renders t in the canonical stored form: UTC, fixed-width nanos
func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}

// SQLite connection pool tuning
const (
	// maxOpenConns is 1 because SQLite is single-writer — one Go-level connection
	// serialises access and avoids SQLITE_BUSY lock contention
	maxOpenConns = 1
	maxIdleConns = 1
	// connMaxLifetime of 0 means connections live forever, acceptable with maxOpenConns=1
	connMaxLifetime = 0
)

// DB wraps *sql.DB with a BulkWriter for database operations
// BulkWriter is nil for LocationDB connections
type DB struct {
	SQL    *sqlx.DB
	Writer *BulkWriter
}

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

// Close doesn't Checkpoint: it runs on every quit, including a bare cancel
// with nothing to flush, and that cost turned "let me out" into a stall.
func (d *DB) Close() error {
	if d.Writer != nil {
		d.Writer.Close()
	}
	return d.SQL.Close()
}

// Checkpoint rebuilds planner stats and flushes the WAL into the main file.
// Called after every workflow phase, keeping each phase's WAL small instead
// of letting it grow across the whole run.
func (d *DB) Checkpoint() error {
	if _, err := d.SQL.Exec("PRAGMA optimize"); err != nil {
		return fmt.Errorf("pragma optimize: %w", err)
	}
	// WAL mode doesn't fold -wal/-shm back into the main file just because
	// the connection closes — force a full checkpoint so a clean shutdown
	// doesn't leave them behind.
	if _, err := d.SQL.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("wal checkpoint: %w", err)
	}
	return nil
}

// openAppDB opens the application SQLite database, applies pragma tuning,
// runs migrations, and initialises the BulkWriter for batched writes
func openAppDB(dbPath string, log logger.Logger) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", appDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("unable to open database: %w", err)
	}

	// Pin the pool before the first query, not after the pragmas: every
	// connection-scoped setting now rides in the DSN, and this makes sure
	// there is only ever the one connection carrying them.
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)

	appID := appIDFromTag()
	if err := verifyAppID(sqlDB, dbPath, appID); err != nil {
		sqlDB.Close()
		return nil, err
	}

	// What is left here is database-scoped: it is written into the file's own
	// header once and every later connection reads it back, so it belongs in a
	// statement rather than in the DSN.
	pragmas := []string{
		"PRAGMA page_size=32768",         //  32KB for better I/O efficiency
		"PRAGMA journal_mode=WAL",        // Better concurrency and durability
		"PRAGMA auto_vacuum=INCREMENTAL", // Enables incremental space reclamation

		fmt.Sprintf("PRAGMA application_id=%d", appID), // Unique identifier for the application
	}

	for _, pragma := range pragmas {
		if _, err := sqlDB.Exec(pragma); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("setting pragma %q: %w", pragma, err)
		}
	}

	if err := assertPragmas(sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}

	log.Info("Database connection established", "path", dbPath)

	sqlxDB := sqlx.NewDb(sqlDB, "sqlite")

	// A migration rewrites the user's only record of their library, so an
	// existing library is backed up first, beside the regular backup (not
	// over it). A fresh database has nothing to lose; a newer one is refused.
	pending, applied, err := migrations.Pending(sqlxDB)
	if err != nil {
		sqlxDB.Close()
		return nil, fmt.Errorf("appDB: %w", err)
	}
	if pending > 0 && applied > 0 {
		dest := filepath.Join(filepath.Dir(dbPath), PreMigrationBackupFileName)
		if err := (&DB{SQL: sqlxDB}).Backup(context.Background(), dest); err != nil {
			sqlxDB.Close()
			return nil, fmt.Errorf("appDB: back up before upgrading the database (nothing was changed): %w", err)
		}
		log.Info("Backed up the library database before upgrading it", "backup", dest)
	}

	count, err := migrations.Run(sqlxDB)
	if err != nil {
		sqlxDB.Close()
		return nil, fmt.Errorf("appDB: migrations - %w", err)
	}

	log.Info("Migration completed", "migrations", count)
	log.Info("Successfully connected to sqlite database", "path", dbPath)
	d := &DB{SQL: sqlxDB}
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
	return &DB{SQL: sqlx.NewDb(sqlDB, "sqlite")}, nil
}

// Optimize reclaims SQLite disk space (incremental_vacuum) and releases
// internal memory (shrink_memory). Call after large delete operations.
func (db *DB) Optimize(ctx context.Context) error {
	if _, err := db.SQL.ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
		return fmt.Errorf("incremental vacuum failed: %w", err)
	}
	if _, err := db.SQL.ExecContext(ctx, "PRAGMA shrink_memory"); err != nil {
		return fmt.Errorf("shrink memory failed: %w", err)
	}
	return nil
}

func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sqlx.Tx, error) {
	return db.SQL.BeginTxx(ctx, opts)
}

func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.SQL.ExecContext(ctx, query, args...)
}

func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.SQL.QueryContext(ctx, query, args...)
}

func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.SQL.QueryRowContext(ctx, query, args...)
}

// appDSN spells the library's connection-scoped settings into the DSN so the
// driver applies them to *every* connection it opens, not just to whichever
// one a startup statement happened to land on. These are per-connection
// settings in SQLite: a replacement connection (a retired bad conn, a second
// one opened before the pool was pinned) that missed them would run with
// foreign keys OFF, which turns every cascading delete in this codebase — the
// scanner's sweep, execute's duplicate cleanup, ResetAll — into an orphan-row
// generator, silently. The driver applies _pragma entries in lexicographic
// order, so nothing here may depend on running before anything else here.
func appDSN(dbPath string) string {
	pragmas := []string{
		"busy_timeout(5000)",  // wait 5s on a locked database before failing
		"cache_size(-256000)", // ~256MB page cache (negative = size in KiB)
		"foreign_keys(1)",     // every ON DELETE CASCADE in this codebase
		"fullfsync(1)",        // darwin: flush the drive's own cache, not just the OS
		"journal_size_limit(67108864)",
		// Hold the file lock for the whole session: while wandersort has the
		// library open, any other client — the sqlite3 CLI, a DB browser —
		// gets "database is locked" instead of reading a half-written run or
		// writing under the pipeline. Safe because the pool is one connection.
		"locking_mode(exclusive)",
		// No memory-mapped reads. A library usually lives on an external or
		// network drive; when it drops mid-read, read() returns an error
		// SQLite handles, but a mapped page faults with SIGBUS and the
		// process dies on the spot — mid-execute included. The syscalls
		// mmap saves are not worth an uncontrolled crash.
		"mmap_size(0)",
		// FULL, not NORMAL: under NORMAL, WAL mode does not fsync at commit, so
		// a power loss drops an unbounded tail of *committed* transactions —
		// including the rows saying a photo was copied into the library and its
		// source may be deleted. Writes are batched (see BulkWriter), so the
		// cost is a handful of fsyncs a second, not one per row.
		"synchronous(full)",
		"temp_store(memory)", // temp tables and indices in RAM
		"wal_autocheckpoint(2000)",
	}
	u := url.URL{Scheme: "file", Path: dbPath}
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// assertPragmas reads back the two settings whose silent absence would be a
// correctness bug rather than a slowdown. A DSN typo, or a driver that stops
// honouring _pragma, otherwise costs an invariant with no symptom until the
// first orphaned row.
func assertPragmas(sqlDB *sql.DB) error {
	var fk int
	if err := sqlDB.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("reading foreign_keys: %w", err)
	}
	if fk != 1 {
		return fmt.Errorf("foreign keys are off on this connection; refusing to open the library")
	}
	var sync int
	if err := sqlDB.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		return fmt.Errorf("reading synchronous: %w", err)
	}
	// 2 is FULL, 3 is EXTRA; anything below 2 does not fsync at commit.
	if sync < 2 {
		return fmt.Errorf("database commits are not durable (synchronous=%d); refusing to open the library", sync)
	}
	return nil
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
