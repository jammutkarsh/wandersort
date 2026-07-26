// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/deps"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

const (
	// LocationDownloadBaseURL is the download URL for the locationDB asset
	// Upstream update schedules and data details can be found at the source URL
	LocationDownloadBaseURL = "https://locationdb.utkarshchourasia.in"

	LocationDBFileName = "location.db"

	// locationMetaFileName is the metadata JSON published alongside the LocationDB
	// location.json holds the dynamic metadata (version, date) used to determine if a re-download is required
	LocationMetaFileName = "location.json"
)

// Setup downloads the location database and its metadata if they do not exist
// onProgress (may be nil) reports (bytesDownloaded, totalBytes) while fetching
// the location database, for a TUI progress bar — the per-byte counts stay out
// of the file log, which only records the start/done milestones.
func Setup(ctx context.Context, log logger.Logger, dbPath string, onProgress func(done, total int64)) error {
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
	// next, and location.New verifies against it when opening
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

// Open downloads the location database if missing, opens it, and returns a
// verified Resolver plus the underlying *db.DB (the caller owns closing it).
// This is the single download-open-verify path — application code
// (cli.App.InitLocationResolver) and tests (pkg/location/locationtest) both
// go through it, so a test exercising a Resolver exercises the exact same
// setup the app performs, not a hand-rolled approximation of it.
func Open(ctx context.Context, log logger.Logger, dbPath string, onProgress func(done, total int64)) (*Resolver, *db.DB, error) {
	if err := Setup(ctx, log, dbPath, onProgress); err != nil {
		return nil, nil, fmt.Errorf("location db: %w", err)
	}

	locationDB, err := db.New(ctx, dbPath, db.LocationDB, log)
	if err != nil {
		return nil, nil, fmt.Errorf("location db: %w", err)
	}

	resolver, err := New(locationDB, dbPath, log)
	if err != nil {
		locationDB.Close()
		return nil, nil, fmt.Errorf("location resolver: %w", err)
	}
	return resolver, locationDB, nil
}
