// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package exif runs exiftool over every hashed file and fills in its
// file_metadata EXIF columns. Never touches the hash — each phase claims
// its own rows.
package exif

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// hashedFiles is the phase's work set: files this session hashed but has not
// read metadata from yet. Sidecars (iPhone .AAE edit files) carry no EXIF of
// their own, so they are never claimed and stay at HASHED
const hashedFiles = `
	FROM live_files
	WHERE scan_status = ?
		AND COALESCE(media_type, '') != ?`

// Extractor reads EXIF from hashed files and persists it
type Extractor struct {
	db      *db.DB
	log     logger.Logger
	pool    *exiftool.Pool
	workers int
}

func New(db *db.DB, log logger.Logger, exiftoolPath string, workers int) *Extractor {
	pool, err := exiftool.NewPool(exiftoolPath, workers)
	if err != nil {
		// An unavailable exiftool binary is not fatal: the phase still marks
		// every file ANALYZED with empty metadata so the pipeline can proceed.
		log.Warn("Exiftool unavailable; metadata will be empty", "error", err)
	}

	return &Extractor{
		db:      db,
		log:     log,
		pool:    pool,
		workers: workers,
	}
}

// Run claims every hashed file in pages and extracts its metadata in a
// bounded worker pool. Returns how many files were persisted
func (e *Extractor) Run(ctx context.Context) (int, error) {
	if e.pool != nil {
		defer e.pool.Close()
	}

	toExtract := make(chan fileRecord, 2*e.workers)
	producerErr := make(chan error, 1)

	var extracted atomic.Int64

	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	e.log.Info("Extracting metadata")

	var total int
	if err := e.db.QueryRowContext(ctx, `SELECT COUNT(*) `+hashedFiles,
		db.StatusHashed, classifier.MediaTypeSidecar).Scan(&total); err != nil {
		e.log.Warn("Failed to count files to extract", "error", err)
	}

	go e.producer(ctxWithCancel, cancel, toExtract, producerErr)

	// Workers write straight through the BulkWriter rather than funnelling into
	// a store goroutine: the writer already serializes every operation, so the
	// extra hop would only add a channel
	var wg sync.WaitGroup
	for range e.workers {
		wg.Go(func() {
			e.worker(ctxWithCancel, toExtract, &extracted, total)
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
	e.log.Info("Metadata extraction complete", "filesExtracted", persisted)
	return persisted, nil
}

// producer claims hashed files one at a time and feeds them to the workers
func (e *Extractor) producer(ctx context.Context, cancel context.CancelFunc, toExtract chan<- fileRecord, producerErr chan<- error) {
	defer close(toExtract)

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
		case toExtract <- record:
		case <-ctx.Done():
			producerErr <- ctx.Err()
			return
		}
	}
}

// getFile atomically claims the next hashed file and returns its record. The
// ANALYZING stamp is what makes an interrupted phase resumable: the next scan
// resets those rows to HASHED instead of re-hashing them
func (e *Extractor) getFile(ctx context.Context) (fileRecord, bool, error) {
	var id int64
	var fileDir, fileName string
	query := `
	UPDATE file_registry
	SET scan_status = ?
	WHERE id = (
		SELECT id ` + hashedFiles + `
		ORDER BY id
		LIMIT 1
	)
	RETURNING id, file_dir, file_name`

	err := e.db.
		QueryRowContext(ctx, query, db.StatusAnalyzing, db.StatusHashed, classifier.MediaTypeSidecar).
		Scan(&id, &fileDir, &fileName)
	if errors.Is(err, sql.ErrNoRows) {
		return fileRecord{}, false, nil
	}
	if err != nil {
		return fileRecord{}, false, fmt.Errorf("claim next exif row: %w", err)
	}

	return fileRecord{id: id, absPath: filepath.Join(fileDir, fileName)}, true, nil
}

// worker extracts one file at a time and enqueues its metadata write
func (e *Extractor) worker(ctx context.Context, toExtract <-chan fileRecord, extracted *atomic.Int64, total int) {
	for file := range toExtract {
		if ctx.Err() != nil {
			return
		}

		// A failed extraction is not a failed file: the pipeline still knows the
		// file's hash and its folder context, so the VFS can place it. Persist
		// the empty metadata and move on
		var meta classifier.CommonMetadata
		var err error
		if e.pool != nil {
			meta, err = e.pool.Extract(ctx, file.absPath)
		} else {
			err = fmt.Errorf("exiftool not available")
		}
		if err != nil {
			// A cancelled pipeline SIGKILLs the exiftool child ("signal: killed")
			// and fails the next call with "context canceled" — that is shutdown,
			// not a bad file, so don't report it as an extraction failure.
			if ctx.Err() != nil {
				return
			}
			e.log.Warn("Failed to extract exif data", "fileId", file.id, "path", file.absPath, "error", err)
		}

		// StreamKey: feeds the TUI progress bar, stripped from the plain console.
		e.log.Info("Reading metadata", logger.StreamKey, true,
			"file", filepath.Base(file.absPath), "extracted", extracted.Add(1), "total", total)

		if !e.db.Writer.Write(e.store(file.id, meta)) {
			e.log.Warn("Bulk writer closed; dropping metadata write", "fileId", file.id)
			return
		}
	}
}

// store builds the write that fills in the row the hash phase inserted
func (e *Extractor) store(fileID int64, meta classifier.CommonMetadata) db.DBOperation {
	isScreenshot := 0
	if meta.IsScreenshot {
		isScreenshot = 1
	}

	return func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(
			ctx, `
			UPDATE file_metadata SET
				exif_image_width = ?, exif_image_height = ?, exif_orientation = ?,
				exif_gps_latitude = ?, exif_gps_longitude = ?,
				exif_make = ?, exif_model = ?,
				exif_date_time_original = ?, exif_create_date = ?, exif_creation_date = ?,
				is_screenshot = ?
			WHERE file_id = ?`,
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
			fileID,
		); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE file_registry SET scan_status = ? WHERE id = ?`, db.StatusAnalyzed, fileID)
		return err
	}
}
