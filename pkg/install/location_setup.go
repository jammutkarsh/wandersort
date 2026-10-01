package install

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

// This file is the location-database half of pkg/install's job: pkg/location
// only ever queries an already-open, already-verified *db.ReadOnly.

const (
	// LocationDownloadBaseURL is the download URL for the locationDB asset.
	// Upstream update schedules and data details can be found at the source URL.
	LocationDownloadBaseURL = "https://locationdb.utkarshchourasia.in"

	// locationDBArchiveSuffix: the database ships zstd-compressed (pure-Go
	// zstd decodes far faster than xz)
	locationDBArchiveSuffix = ".zst"

	// LocationMetaFileName is the published metadata: version, date, checksum
	// and row counts verifyLocationDB checks
	LocationMetaFileName = "location.json"
)

// locationMeta is LocationMetaFileName's shape.
type locationMeta struct {
	Hash string         `json:"sha256"`
	Rows map[string]int `json:"rows"`
}

// downloadLocationDB fetches the location database and its metadata if
// missing. onProgress may be nil.
func downloadLocationDB(ctx context.Context, log logger.Logger, dbPath string, onProgress func(done, total int64)) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create dir %q: %w", dbPath, err)
	}

	if _, err := os.Stat(dbPath); err == nil {
		log.Info("location db found", "path", dbPath)
		return nil
	}

	log.Info("Downloading location database", logger.UserKey, true,
		logger.PhaseKey, "location", logger.EventKey, "start",
		"dir", path.New().RelativeToHome(dbPath))

	// metadata first, and required: without it the database fails
	// verification forever while never being re-downloaded
	metaPath := filepath.Join(filepath.Dir(dbPath), LocationMetaFileName)
	if err := downloadFile(ctx, metaPath, LocationDownloadBaseURL+"/"+LocationMetaFileName, "", nil); err != nil {
		return fmt.Errorf("download %s: %w", LocationMetaFileName, err)
	}

	archiveName := config.LocationDBFileName + locationDBArchiveSuffix
	archivePath := dbPath + locationDBArchiveSuffix
	// no digest here: the expected hash is of the decompressed db and ships
	// in the metadata file above, which verifyLocationDB checks against
	if err := downloadFile(ctx, archivePath, LocationDownloadBaseURL+"/"+archiveName, "", onProgress); err != nil {
		return fmt.Errorf("download %s: %w", archiveName, err)
	}
	// logged: nothing else reports progress during decompression
	log.Info("Decompressing location database", logger.UserKey, true,
		logger.PhaseKey, "location", logger.EventKey, "decompress")
	if err := decompressZstd(archivePath, dbPath); err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("decompress %s: %w", archiveName, err)
	}
	if err := os.Remove(archivePath); err != nil {
		log.Warn("failed to remove downloaded archive", "path", archivePath, "error", err)
	}

	log.Info("location database downloaded", logger.UserKey, true,
		logger.PhaseKey, "location", logger.EventKey, "done")
	return nil
}

// decompressZstd streams archivePath into dest via a temp file and rename, so
// a crash never leaves a truncated database.
func decompressZstd(archivePath, dest string) error {
	zr, closeZr, err := openZstd(archivePath)
	if err != nil {
		return err
	}
	defer closeZr()

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dl-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op if Rename succeeded
	}()

	if _, err := io.Copy(tmp, zr); err != nil {
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, dest, err)
	}
	return nil
}

// verifyLocationDB checks a database's checksum and the row count of every
// table its metadata names.
func verifyLocationDB(dbPath string, locationDB *db.ReadOnly, log logger.Logger) error {
	metaPath := filepath.Join(filepath.Dir(dbPath), LocationMetaFileName)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("unable to read location meta: %w", err)
	}

	var meta locationMeta
	// published by the location database's own repo, not this one: match its
	// keys the forgiving way
	if err := json.Unmarshal(data, &meta, json.MatchCaseInsensitiveNames(true)); err != nil {
		return fmt.Errorf("unable to parse location meta: %w", err)
	}

	sum, err := fileSHA256(dbPath)
	if err != nil {
		return fmt.Errorf("checksum location db: %w", err)
	}
	if sum != meta.Hash {
		return fmt.Errorf("location db checksum mismatch: got %s, want %s", sum, meta.Hash)
	}
	log.Info("location db checksum verified", "path", dbPath, "hash", sum)

	// every table meta.Rows names is checked
	for table, want := range meta.Rows {
		var count int
		if err := locationDB.SQL.QueryRowContext(context.Background(),
			fmt.Sprintf(`SELECT COUNT(*) FROM %q`, table)).Scan(&count); err != nil {
			return fmt.Errorf("verifying location database table %s: %w", table, err)
		}
		if count != want {
			return fmt.Errorf("row count mismatch in %s: db has %d, meta expects %d", table, count, want)
		}
		log.Info("location db table verified", "path", dbPath, "table", table, "rows", count)
	}
	return nil
}

func removeIfExists(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// OpenLocationResolver downloads (if missing), verifies and opens the location
// database. The caller owns closing the returned *db.ReadOnly.
func OpenLocationResolver(ctx context.Context, log logger.Logger, dbPath string, onProgress func(done, total int64)) (*location.Resolver, *db.ReadOnly, error) {
	if err := downloadLocationDB(ctx, log, dbPath, onProgress); err != nil {
		return nil, nil, fmt.Errorf("location db: %w", err)
	}

	locationDB, err := db.OpenReadOnly(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("location db: %w", err)
	}

	if err := verifyLocationDB(dbPath, locationDB, log); err != nil {
		locationDB.Close()
		// remove a database that fails verification, or every later start
		// fails the same way (the download is skipped while it exists)
		metaPath := filepath.Join(filepath.Dir(dbPath), LocationMetaFileName)
		if rerr := errors.Join(removeIfExists(dbPath), removeIfExists(metaPath)); rerr != nil {
			log.Warn("could not remove the location database that failed verification", "error", rerr)
		}
		return nil, nil, fmt.Errorf("location resolver: %w (removed; it will be downloaded again next time)", err)
	}
	return location.NewResolver(locationDB, log), locationDB, nil
}
