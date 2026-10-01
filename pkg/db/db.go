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

// TimeLayout is wall-clock time with fixed-width nanoseconds and no zone, so
// string order in SQL is time order. Every stored time is wall clock.
const TimeLayout = "2006-01-02T15:04:05.000000000"

// FormatTime renders t's wall-clock reading in the stored form.
func FormatTime(t time.Time) string {
	return t.Format(TimeLayout)
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

// DB is an open database. Reads use SQL; every write goes through Writer
// (Write batched, WriteSync for an outcome), so writes stay in one order and
// their failures are reported. Writer is nil for the location database.
type DB struct {
	SQL    *sqlx.DB
	Writer *BulkWriter
}

// New opens (creating if needed) the library database at dbPath, migrates it,
// and starts its writer. ctx bounds the backup taken before a migration.
func New(ctx context.Context, dbPath string, log logger.Logger) (*DB, error) {
	return openAppDB(ctx, dbPath, log)
}

// Close doesn't Checkpoint: it runs on every quit, including a bare cancel
// with nothing to flush, and that cost turned "let me out" into a stall.
func (d *DB) Close() error {
	if d.Writer != nil {
		d.Writer.Close()
	}
	return d.SQL.Close()
}

// Checkpoint rebuilds planner stats and flushes the WAL; called after every
// workflow phase.
func (d *DB) Checkpoint() error {
	if _, err := d.SQL.Exec("PRAGMA optimize"); err != nil {
		return fmt.Errorf("pragma optimize: %w", err)
	}
	// a clean shutdown must not leave -wal/-shm behind
	if _, err := d.SQL.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("wal checkpoint: %w", err)
	}
	return nil
}

// openAppDB opens the application SQLite database, applies pragma tuning,
// runs migrations, and initialises the BulkWriter for batched writes
func openAppDB(ctx context.Context, dbPath string, log logger.Logger) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", appDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("unable to open database: %w", err)
	}

	// pin the pool before the first query: one connection carries the DSN
	// pragmas
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)

	appID := appIDFromTag()
	if err := verifyAppID(sqlDB, dbPath, appID); err != nil {
		sqlDB.Close()
		return nil, err
	}

	// database-scoped settings, stored in the file header
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

	// back up an existing library before migrating it; a newer schema is
	// refused
	pending, applied, err := migrations.Pending(sqlxDB)
	if err != nil {
		sqlxDB.Close()
		return nil, fmt.Errorf("appDB: %w", err)
	}
	if pending > 0 && applied > 0 {
		dest := filepath.Join(filepath.Dir(dbPath), PreMigrationBackupFileName)
		if err := (&DB{SQL: sqlxDB}).Backup(ctx, dest); err != nil {
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

// OpenLocation opens the read-only location database. It has no writer and
// no migrations: the file is downloaded whole and never written.
func OpenLocation(dbPath string, log logger.Logger) (*DB, error) {
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

func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.SQL.QueryContext(ctx, query, args...)
}

func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.SQL.QueryRowContext(ctx, query, args...)
}

// appDSN puts the connection-scoped pragmas in the DSN so every connection
// gets them. Without foreign_keys a replacement connection would silently
// turn every ON DELETE CASCADE into orphan rows. The driver applies _pragma
// entries in lexicographic order, so none may depend on another.
func appDSN(dbPath string) string {
	pragmas := []string{
		"busy_timeout(5000)",  // wait 5s on a locked database before failing
		"cache_size(-256000)", // ~256MB page cache (negative = size in KiB)
		"foreign_keys(1)",     // every ON DELETE CASCADE in this codebase
		"fullfsync(1)",        // darwin: flush the drive's own cache, not just the OS
		"journal_size_limit(67108864)",
		// hold the file lock all session: other clients get "database is
		// locked". Safe because the pool is one connection.
		"locking_mode(exclusive)",
		// no mmap: a library on an external drive that drops mid-read SIGBUSes
		// a mapped page, while read() returns an error SQLite handles
		"mmap_size(0)",
		// FULL, not NORMAL: in WAL mode NORMAL doesn't fsync at commit, so a
		// power loss could drop committed "file is placed" rows
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

// assertPragmas reads back the settings whose silent absence would be a
// correctness bug (foreign_keys, synchronous).
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

// verifyAppID refuses a non-empty sqlite file whose application_id isn't
// ours. A fresh file passes and is stamped.
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
