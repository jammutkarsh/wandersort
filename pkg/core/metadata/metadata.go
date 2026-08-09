// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package metadata is the pipeline's single read pass over every discovered
// file: one worker hashes the bytes and then runs exiftool over the same file,
// back to back, so the header read exiftool needs hits the page cache the hash
// just warmed. Hashing and EXIF used to be two phases, which meant reading
// every file twice — on a library big enough to matter, far enough apart that
// the cache had already evicted it, so the second pass paid full disk cost.
package metadata

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"lukechampine.com/blake3"
)

// hashOutputSize is the output length of BLAKE3-256 in bytes
const hashOutputSize = 32

// fileRecord is what the claim query hands a worker: mediaType is carried so a
// sidecar can be hashed without paying for an exiftool call it has no tags for
type fileRecord struct {
	id        int64
	absPath   string
	mediaType string
}

// Extractor hashes discovered files and reads their EXIF in one pass
type Extractor struct {
	db      *db.DB
	log     logger.Logger
	pool    *exiftool.Pool
	workers int
}

func New(database *db.DB, log logger.Logger, exiftoolPath string, workers int) *Extractor {
	pool, err := exiftool.NewPool(exiftoolPath, workers)
	if err != nil {
		// An unavailable exiftool binary is not fatal: files still get hashed
		// and persisted with empty metadata so the pipeline can proceed.
		log.Warn("Exiftool unavailable; metadata will be empty", "error", err)
	}

	return &Extractor{
		db:      database,
		log:     log,
		pool:    pool,
		workers: workers,
	}
}

// Run claims every DISCOVERED file one at a time and reads it in a bounded
// worker pool. Returns how many files were persisted
func (e *Extractor) Run(ctx context.Context) (int, error) {
	if e.pool != nil {
		defer e.pool.Close()
	}

	toRead := make(chan fileRecord, 2*e.workers)
	producerErr := make(chan error, 1)

	var extracted atomic.Int64

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	e.log.Info("Extracting metadata")

	var total int
	if err := e.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM live_files WHERE scan_status = ?
	`, db.StatusDiscovered).Scan(&total); err != nil {
		e.log.Warn("Failed to count files to read", "error", err)
	}

	go e.producer(ctxWithCancel, cancel, toRead, producerErr)

	// Workers write straight through the BulkWriter rather than funnelling into
	// a store goroutine: the writer already serializes every operation, so the
	// extra hop would only add a channel
	var wg sync.WaitGroup
	for range e.workers {
		wg.Go(func() {
			e.worker(ctxWithCancel, toRead, &extracted, total)
		})
	}
	wg.Wait()

	if err := <-producerErr; err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	persisted := int(extracted.Load())
	e.log.Info("Metadata extraction complete", "filesRead", persisted)
	return persisted, nil
}

// producer claims discovered files one at a time and feeds them to the workers
func (e *Extractor) producer(ctx context.Context, cancel context.CancelFunc, toRead chan<- fileRecord, producerErr chan<- error) {
	defer close(toRead)

	for {
		record, ok, err := e.getFile(ctx)
		if err != nil {
			producerErr <- err
			cancel()
			return
		}
		if !ok {
			producerErr <- nil
			return
		}

		select {
		case toRead <- record:
		case <-ctx.Done():
			producerErr <- ctx.Err()
			return
		}
	}
}

// getFile atomically claims the next discovered file. The ANALYZING stamp is
// what makes an interrupted phase resumable: the scanner's upsert resets those
// rows to DISCOVERED, since nothing was persisted for them
func (e *Extractor) getFile(ctx context.Context) (fileRecord, bool, error) {
	var id int64
	var fileDir, fileName, mediaType string
	query := `
	UPDATE file_registry
	SET scan_status = ?
	WHERE id = (
		SELECT id
		FROM live_files
		WHERE scan_status = ?
		ORDER BY id
		LIMIT 1
	)
	RETURNING id, file_dir, file_name, COALESCE(media_type, '')`

	err := e.db.
		QueryRowContext(ctx, query, db.StatusAnalyzing, db.StatusDiscovered).
		Scan(&id, &fileDir, &fileName, &mediaType)
	if errors.Is(err, sql.ErrNoRows) {
		return fileRecord{}, false, nil
	}
	if err != nil {
		return fileRecord{}, false, fmt.Errorf("claim next metadata row: %w", err)
	}

	return fileRecord{id: id, absPath: filepath.Join(fileDir, fileName), mediaType: mediaType}, true, nil
}

// worker hashes one file, reads its EXIF while the bytes are still cached, and
// enqueues the single write that persists both
func (e *Extractor) worker(ctx context.Context, toRead <-chan fileRecord, extracted *atomic.Int64, total int) {
	for file := range toRead {
		if ctx.Err() != nil {
			return
		}

		hash, err := hashFile(file.absPath)
		if err != nil {
			e.log.Error("Failed to hash file", "fileId", file.id, "path", file.absPath, "error", err)
			e.db.Writer.Write(storeFailure(file.id))
			continue
		}

		// A failed extraction is not a failed file: the pipeline still knows the
		// file's hash and its folder context, so the VFS can place it. Persist
		// the empty metadata and move on
		var meta classifier.CommonMetadata
		// Sidecars (iPhone .AAE edit files) carry no EXIF of their own, so
		// spawning exiftool on them is pure waste — hash them and move on
		if file.mediaType != classifier.MediaTypeSidecar {
			if e.pool != nil {
				meta, err = e.pool.Extract(ctx, file.absPath)
			} else {
				err = fmt.Errorf("exiftool not available")
			}
			if err != nil {
				// A cancelled pipeline SIGKILLs the exiftool child ("signal:
				// killed") and fails the next call with "context canceled" —
				// that is shutdown, not a bad file, so don't report it as an
				// extraction failure.
				if ctx.Err() != nil {
					return
				}
				e.log.Warn("Failed to extract exif data", "fileId", file.id, "path", file.absPath, "error", err)
			}
		}

		// StreamKey: feeds the TUI progress bar, stripped from the plain console.
		e.log.Info("Reading", logger.StreamKey, true,
			"file", filepath.Base(file.absPath), "extracted", extracted.Add(1), "total", total)

		if !e.db.Writer.Write(e.store(file.id, hash, meta)) {
			e.log.Warn("Bulk writer closed; dropping metadata write", "fileId", file.id)
			return
		}
	}
}

// hashFile computes the BLAKE3 hash of a file
// Uses streaming to handle files of any size with constant memory (~32KB buffer)
func hashFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := blake3.New(hashOutputSize, nil)

	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	sum := make([]byte, 0, hashOutputSize)
	return hex.EncodeToString(hasher.Sum(sum)), nil
}

// store writes the hash and the EXIF columns as one row and marks the file read
func (e *Extractor) store(fileID int64, hash string, meta classifier.CommonMetadata) db.DBOperation {
	isScreenshot := 0
	if meta.IsScreenshot {
		isScreenshot = 1
	}

	return func(ctx context.Context, tx *sqlx.Tx) error {
		// A re-read file still has its old metadata row; a plain INSERT would
		// violate UNIQUE(file_hash, file_id) or leave a stale duplicate. No-op
		// for fresh files.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM file_metadata WHERE file_id = ?`, fileID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO file_metadata (
				file_hash, file_id,
				exif_image_width, exif_image_height, exif_orientation,
				exif_gps_latitude, exif_gps_longitude,
				exif_make, exif_model,
				exif_date_time_original, exif_create_date, exif_creation_date,
				is_screenshot
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			hash,
			fileID,
			db.IntOrNil(meta.ImageWidth),
			db.IntOrNil(meta.ImageHeight),
			db.IntOrNil(meta.Orientation),
			db.FloatOrNil(meta.GPSLatitude),
			db.FloatOrNil(meta.GPSLongitude),
			db.StrOrNil(meta.Make),
			db.StrOrNil(meta.Model),
			db.StrOrNil(meta.DateTimeOriginal),
			db.StrOrNil(meta.CreateDate),
			db.StrOrNil(meta.CreationDate),
			isScreenshot,
		); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE file_registry SET scan_status = ? WHERE id = ?`, db.StatusAnalyzed, fileID)
		return err
	}
}

// storeFailure drops the file's stale metadata and parks it at ERROR: its
// content is unknown now, so an old hash and old EXIF would keep feeding the
// scorer and VFS data that no longer describes the bytes on disk
func storeFailure(fileID int64) db.DBOperation {
	return func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM file_metadata WHERE file_id = ?`, fileID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE file_registry SET scan_status = ? WHERE id = ?`, db.StatusError, fileID)
		return err
	}
}
