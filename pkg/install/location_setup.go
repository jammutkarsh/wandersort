// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

// This file is the location-database half of pkg/install's job: pkg/location
// only ever queries an already-open, already-verified *db.DB.

const (
	// LocationDownloadBaseURL is the download URL for the locationDB asset.
	// Upstream update schedules and data details can be found at the source URL.
	LocationDownloadBaseURL = "https://locationdb.utkarshchourasia.in"

	LocationDBFileName = "location.db"

	// locationDBArchiveSuffix is appended to LocationDBFileName for the
	// remote asset name and the local download target: the DB now ships
	// zstd-compressed (fuzzy search needs the bigger geonames_trigrams
	// table, see pkg/location's SearchByName, and that only stays a
	// reasonable download size compressed). zstd over xz: decode speed
	// barely moves with compression level and a pure-Go decoder
	// (klauspost/compress) still runs orders of magnitude faster than a
	// pure-Go xz decoder — a real difference for a ~400MB database decoded
	// on every install. There is no uncompressed .db file on the remote any
	// more.
	locationDBArchiveSuffix = ".zst"

	// LocationMetaFileName is the metadata JSON published alongside the
	// location database: version, date, and the checksum/row-counts
	// verifyLocationDB checks a downloaded (and decompressed) copy against.
	LocationMetaFileName = "location.json"
)

// locationMeta is LocationMetaFileName's shape.
type locationMeta struct {
	Hash string         `json:"sha256"`
	Rows map[string]int `json:"rows"`
}

// downloadLocationDB fetches the location database and its metadata if they
// don't already exist. onProgress (may be nil) reports byte progress for a
// TUI bar; the file log only records start/done milestones.
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

	archiveName := LocationDBFileName + locationDBArchiveSuffix
	archivePath := dbPath + locationDBArchiveSuffix
	// no digest here: the expected hash is of the decompressed db and ships
	// in the metadata file downloaded next, which verifyLocationDB checks
	// against once both are on disk
	if err := downloadFile(ctx, log, archivePath, LocationDownloadBaseURL+"/"+archiveName, "", onProgress); err != nil {
		return fmt.Errorf("download %s: %w", archiveName, err)
	}
	// zstd decodes this in low single-digit seconds even in pure Go, but
	// still logged (unlike exiftool's much smaller archive) since nothing
	// else reports progress between the download bar finishing and this
	// step's own completion.
	log.Info("Decompressing location database", logger.UserKey, true,
		logger.PhaseKey, "location", logger.EventKey, "decompress")
	if err := decompressZstd(archivePath, dbPath); err != nil {
		os.Remove(archivePath)
		return fmt.Errorf("decompress %s: %w", archiveName, err)
	}
	if err := os.Remove(archivePath); err != nil {
		log.Warn("failed to remove downloaded archive", "path", archivePath, "error", err)
	}

	metaPath := filepath.Join(filepath.Dir(dbPath), LocationMetaFileName)
	if err := downloadFile(ctx, log, metaPath, LocationDownloadBaseURL+"/"+LocationMetaFileName, "", nil); err != nil {
		log.Warn("location db: could not download metadata (non-fatal)", "file", LocationMetaFileName, "error", err)
	}

	log.Info("location database downloaded", logger.UserKey, true,
		logger.PhaseKey, "location", logger.EventKey, "done")
	return nil
}

// decompressZstd streams archivePath through openZstd into dest, writing
// through a temp file in dest's own directory so a crash mid-decompress
// never leaves a truncated location.db behind — same atomic
// temp-file-then-rename pattern downloadFile itself uses.
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

// verifyLocationDB checksums a downloaded database against its published
// metadata, then checks the row count on the table Resolver's queries depend
// on. Not UserKey-tagged: runs on every command, not just installs.
func verifyLocationDB(dbPath string, locationDB *db.DB, log logger.Logger) error {
	metaPath := filepath.Join(filepath.Dir(dbPath), LocationMetaFileName)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("unable to read location meta: %w", err)
	}

	var meta locationMeta
	if err := json.Unmarshal(data, &meta); err != nil {
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

	// Every table meta.Rows names gets checked, not just geonames_cities —
	// this is what makes geonames_trigrams (the fuzzy-search index) verified
	// too the moment the published meta grows that key, with no further
	// change needed here.
	for table, want := range meta.Rows {
		var count int
		if err := locationDB.QueryRowContext(context.Background(),
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

// OpenLocationResolver downloads (if missing), verifies, and opens the
// location database, returning a ready Resolver plus the *db.DB (caller
// owns closing it). The single download-open-verify path — installtest
// exercises this exact function, not a hand-rolled approximation.
func OpenLocationResolver(ctx context.Context, log logger.Logger, dbPath string, onProgress func(done, total int64)) (*location.Resolver, *db.DB, error) {
	if err := downloadLocationDB(ctx, log, dbPath, onProgress); err != nil {
		return nil, nil, fmt.Errorf("location db: %w", err)
	}

	locationDB, err := db.New(ctx, dbPath, db.LocationDB, log)
	if err != nil {
		return nil, nil, fmt.Errorf("location db: %w", err)
	}

	if err := verifyLocationDB(dbPath, locationDB, log); err != nil {
		locationDB.Close()
		return nil, nil, fmt.Errorf("location resolver: %w", err)
	}
	return location.NewResolver(locationDB, log), locationDB, nil
}
