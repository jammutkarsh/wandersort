package db

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/db/migrations"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	_ "modernc.org/sqlite"
)

// DBType identifies which database is being opened so Open can apply the
// correct pragmas and setup for each one.
type DBType int

const (
	// WandersortDB is the main application database (read-write, WAL mode,
	// migrations, BulkWriter).
	WandersortDB DBType = iota

	// LocationDB is the GeoNames reverse-geocoding database (read-only).
	// It is downloaded automatically if absent.
	LocationDB
)

const (
	// locationDownloadBaseURL is the download URL for the locationDB asset.
	// Upstream update schedules and data details can be found at the source URL.
	locationDownloadBaseURL = "https://locationdb.utkarshchourasia.in"

	LocationDBFileName = "location.db"

	// locationMetaFileName is the metadata JSON published alongside the LocationDB.
	// location.json holds the dynamic metadata (version, date) used to determine if a re-download is required.
	locationMetaFileName = "location.json"
)

// Open is the single entry point for opening any database and returns a *sql.DB
// ready to be passed into the respective package's init function.
// For WandersortDB: creates the directory, applies WAL pragmas, runs migrations.
// For LocationDB:   downloads the file if absent, opens in read-only mode.
func Open(dbPath string, dbType DBType, log logger.Logger) (*sql.DB, error) {
	switch dbType {
	case WandersortDB:
		return openWandersortDB(dbPath, log)
	case LocationDB:
		return openLocationDB(dbPath, log)
	default:
		return nil, fmt.Errorf("db: unknown DBType %d", dbType)
	}
}

// Close safely closes a wandersort application DB after running optimization
// routines.
func Close(sqlDB *sql.DB) error {
	// PRAGMA optimize runs an analysis to update query planner statistics.
	// It's highly recommended to run this just before closing the database.
	if _, err := sqlDB.Exec("PRAGMA optimize"); err != nil {
		return fmt.Errorf("db: pragma optimize failed: %w", err)
	}
	return sqlDB.Close()
}

func openWandersortDB(dbPath string, log logger.Logger) (*sql.DB, error) {
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
		return nil, fmt.Errorf("db: ping wandersort db: %w", err)
	}

	log.Info("Database connection established", "path", dbPath)

	count, err := migrations.Run(sqlDB)
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: migrations: %w", err)
	}

	log.Info("Migration completed", "migrations", count)
	log.Info("Successfully connected to sqlite database", "path", dbPath)
	return sqlDB, nil
}

func openLocationDB(dbPath string, log logger.Logger) (*sql.DB, error) {
	if err := ensureLocationDB(dbPath, log); err != nil {
		return nil, fmt.Errorf("db: ensure location db: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?mode=ro&_journal=OFF&_sync=OFF", dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open location db: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: ping location db: %w", err)
	}

	log.Info("Successfully connected to location database", "path", dbPath)
	return sqlDB, nil
}

// ensureLocationDB creates the parent directory and downloads location.db if the file does not already exist at dbPath.
func ensureLocationDB(dbPath string, log logger.Logger) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return fmt.Errorf("create dir %q: %w", dbPath, err)
	}

	if _, err := os.Stat(dbPath); err == nil {
		log.Info("location db found; no need to download", "path", dbPath)
		return nil
	}

	log.Info("location db not found; downloading", "url", locationDownloadBaseURL+"/"+LocationDBFileName)

	if err := downloadFile(dbPath, locationDownloadBaseURL+"/"+LocationDBFileName); err != nil {
		return fmt.Errorf("download %s: %w", LocationDBFileName, err)
	}

	metaPath := filepath.Join(filepath.Dir(dbPath), locationMetaFileName)
	if err := downloadFile(metaPath, locationDownloadBaseURL+"/"+locationMetaFileName); err != nil {
		log.Info("location db: could not download metadata (non-fatal)", "file", locationMetaFileName, "error", err)
	}

	return nil
}

// downloadFile fetches url and writes the body to dest atomically (via a temp
// file) so a partial download never leaves a corrupt file at dest.
func downloadFile(dest, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	// Write to a temp file in the same directory so os.Rename is atomic.
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

func appIDFromTag() int32 {
	const tag = "WAND"
	return int32(binary.BigEndian.Uint32([]byte(tag)))
}
