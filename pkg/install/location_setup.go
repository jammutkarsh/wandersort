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
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/deps"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

// This file is the location-database half of "the one place a dependency's
// version, download location, and install layout are known" — pkg/location
// only ever queries an already-open, already-verified *db.DB; it has no idea
// where that file came from or what checksum it's supposed to have.

const (
	// LocationDownloadBaseURL is the download URL for the locationDB asset.
	// Upstream update schedules and data details can be found at the source URL.
	LocationDownloadBaseURL = "https://locationdb.utkarshchourasia.in"

	LocationDBFileName = "location.db"

	// LocationMetaFileName is the metadata JSON published alongside the
	// location database: version, date, and the checksum/row-counts
	// verifyLocationDB checks a downloaded copy against.
	LocationMetaFileName = "location.json"
)

// locationMeta is LocationMetaFileName's shape.
type locationMeta struct {
	Hash string         `json:"sha256"`
	Rows map[string]int `json:"rows"`
}

// downloadLocationDB fetches the location database and its metadata if they
// do not already exist. onProgress (may be nil) reports (bytesDownloaded,
// totalBytes) while fetching the database, for a TUI progress bar — the
// per-byte counts stay out of the file log, which only records the
// start/done milestones.
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

	// no digest here: the expected hash ships in the metadata file downloaded
	// next, and verifyLocationDB checks against it once both are on disk
	if err := deps.Download(ctx, dbPath, LocationDownloadBaseURL+"/"+LocationDBFileName, "", onProgress); err != nil {
		return fmt.Errorf("download %s: %w", LocationDBFileName, err)
	}

	metaPath := filepath.Join(filepath.Dir(dbPath), LocationMetaFileName)
	if err := deps.Download(ctx, metaPath, LocationDownloadBaseURL+"/"+LocationMetaFileName, "", nil); err != nil {
		log.Warn("location db: could not download metadata (non-fatal)", "file", LocationMetaFileName, "error", err)
	}

	log.Info("location database downloaded", logger.UserKey, true,
		logger.PhaseKey, "location", logger.EventKey, "done")
	return nil
}

// verifyLocationDB checks a downloaded location database against its
// published metadata: a byte-for-byte checksum, then a row count on the one
// table location.Resolver's queries depend on. Not UserKey-tagged: this runs
// on every command that opens the resolver, and an integrity check is not a
// milestone. Failures are still hard errors.
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

	sum, err := deps.SHA256File(dbPath)
	if err != nil {
		return fmt.Errorf("checksum location db: %w", err)
	}
	if sum != meta.Hash {
		return fmt.Errorf("location db checksum mismatch: got %s, want %s", sum, meta.Hash)
	}
	log.Info("location db checksum verified", "path", dbPath, "hash", sum)

	var count int
	if err := locationDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM geonames_cities`).Scan(&count); err != nil {
		return fmt.Errorf("verifying location database: %w", err)
	}
	if count != meta.Rows["geonames_cities"] {
		return fmt.Errorf("row count mismatch: db has %d, meta expects %d", count, meta.Rows["geonames_cities"])
	}

	log.Info("location db verified", "path", dbPath, "rows", count)
	return nil
}

// OpenLocationResolver downloads the location database if missing, verifies
// it against its published metadata, and returns a ready location.Resolver
// plus the underlying *db.DB (the caller owns closing it). This is the
// single download-open-verify path — application code (pkg/install.Coordinator)
// and tests (pkg/location/locationtest) both go through it, so a test
// exercising a Resolver exercises the exact same setup the app performs, not
// a hand-rolled approximation of it.
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
