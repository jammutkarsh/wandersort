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

	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/utils"
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

// Installed reports whether the location database file already exists, so the
// caller can skip a download-progress screen when Setup would be a no-op.
func Installed(dbPath string) bool {
	_, err := os.Stat(dbPath)
	return err == nil
}

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

	if err := utils.DownloadFileProgress(ctx, dbPath, LocationDownloadBaseURL+"/"+LocationDBFileName,
		onProgress); err != nil {
		return fmt.Errorf("download %s: %w", LocationDBFileName, err)
	}

	metaPath := filepath.Join(filepath.Dir(dbPath), LocationMetaFileName)
	if err := utils.DownloadFile(ctx, metaPath, LocationDownloadBaseURL+"/"+LocationMetaFileName); err != nil {
		log.Warn("location db: could not download metadata (non-fatal)", "file", LocationMetaFileName, "error", err)
	}

	log.Info("location database downloaded", logger.UserKey, true,
		logger.PhaseKey, "location", logger.EventKey, "done")
	return nil
}
