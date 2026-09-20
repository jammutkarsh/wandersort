// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package scanner

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// Scanner is stateless across runs — mutable state lives in per-call
// locals, so concurrent scans need no locking on Scanner itself.
type Scanner struct {
	db         *db.DB
	classifier *classifier.FileClassifier
	log        logger.Logger
	path       *path.Resolver
	volumes    *volume.Resolver
	workers    int
}

func New(db *db.DB, log logger.Logger, workers int) *Scanner {
	return &Scanner{
		db:         db,
		classifier: classifier.NewFileClassifier(),
		log:        log,
		path:       path.New(),
		volumes:    volume.New(),
		workers:    workers,
	}
}

// Run orchestrates concurrent directory scans across all paths. force replaces
// every touched file's row regardless of size/mtime, so a later phase reads it
// from disk again instead of skipping it as unchanged.
// It returns the total number of files discovered (new + previously seen) and
// any first error encountered
func (s *Scanner) Run(ctx context.Context, paths []string, force bool) (int, error) {
	s.log.Info("Scanner Phase: Processing all paths", "pathCount", len(paths))

	// storeScan stamps last_seen_at after this; sweep uses the gap to tell
	// "not re-seen this run" without needing any session identity.
	scanStartedAt := time.Now()

	type scanResult struct {
		root  string // canonical absolute root, "" when canonicalization failed
		count int
		err   error
	}

	// Buffer exactly one result per path; no path produces more than one result
	results := make(chan scanResult, len(paths))

	// Enqueue all work up front so workers can start immediately without blocking
	jobs := make(chan string, len(paths))
	for _, path := range paths {
		jobs <- path
	}

	// Accept no more jobs; workers will stop when they drain the queue
	close(jobs)

	// Spawn workers to drain the job queue concurrently
	var workers sync.WaitGroup
	for range s.workers {
		workers.Go(func() {
			for path := range jobs {
				// Canonicalize once; every stored file_dir and the sweep must
				// agree on the same absolute root spelling
				absRoot, err := s.path.RealPath(path)
				if err != nil {
					s.log.Error("Failed to resolve path", "path", path, "error", err)
					results <- scanResult{err: fmt.Errorf("resolve %s: %w", path, err)}
					continue
				}

				volumeUUID := s.volumes.ForPath(absRoot)
				discoveredChan, walkErr := s.scan(ctx, absRoot, volumeUUID, force)

				count := 0
				// Drain the channel to both count stored discoveries and wait until
				// scan/store has fully finished this path
				for range discoveredChan {
					count++
				}

				if err := <-walkErr; err != nil {
					s.log.Error("Failed to scan path", "path", absRoot, "error", err)
					results <- scanResult{root: absRoot, count: count, err: fmt.Errorf("scan failed for %s: %w", path, err)}
					continue
				}

				s.log.Info("Scanned path", "path", absRoot, "filesDiscovered", count)
				results <- scanResult{root: absRoot, count: count}
			}
		})
	}

	// Close results only after all workers are done writing to it
	workers.Wait()
	close(results)

	// Flush before sweeping: dbWritesWG tracks upsert execution inside the
	// batch transaction, not its commit, so only a writer flush guarantees the
	// sweep's own statement sees every last_seen_at update
	s.db.Writer.Flush()

	totalFiles := 0
	var firstScanErr error
	for result := range results {
		totalFiles += result.count
		if result.err != nil {
			if firstScanErr == nil {
				firstScanErr = result.err
			}
			continue
		}
		if err := s.sweep(ctx, scanStartedAt, result.root); err != nil {
			s.log.Error("Failed to sweep path", "path", result.root, "error", err)
			if firstScanErr == nil {
				firstScanErr = err
			}
		}
	}

	return totalFiles, firstScanErr
}

// scan walks absRoot and returns a discovery channel plus a single-shot
// error channel that resolves once the discovery channel is drained.
func (s *Scanner) scan(ctx context.Context, absRoot, volumeUUID string, force bool) (<-chan FileDiscovery, <-chan error) {
	s.log.Info("Scanning path", "path", absRoot)
	fileDiscoveryChannel := make(chan FileDiscovery, 2*s.workers)
	scanResultsChannel := make(chan FileDiscovery, 2*s.workers)
	walkErr := make(chan error, 1)

	// Separate channels decouple the walk from the DB write, so a slow
	// writer never blocks directory traversal.

	// Producer
	go func() {
		defer close(scanResultsChannel)

		err := s.walkRoot(ctx, absRoot, volumeUUID, scanResultsChannel)
		if err != nil {
			s.log.Error("Walk root failed", "path", absRoot, "error", err)
		}
		walkErr <- err
	}()

	// Consumer
	go func() {
		s.store(ctx, scanResultsChannel, fileDiscoveryChannel, force)
	}()

	return fileDiscoveryChannel, walkErr
}

// walkRoot walks absRoot (already canonical) and emits FileDiscovery records
// carrying the file's absolute directory and name
func (s *Scanner) walkRoot(ctx context.Context, absRoot, volumeUUID string, output chan<- FileDiscovery) error {
	err := filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Handle errors (permission denied, etc.)
		if err != nil {
			// An unreadable root means the whole scan of this path failed —
			// it must not look like "root is empty" (which would sweep the index)
			if p == absRoot {
				return fmt.Errorf("root unreadable: %w", err)
			}
			s.log.Error("Walk error", "inputPath", absRoot, "walkingPath", s.path.RelativeToHome(p), "error", err)
			return nil // Continue walking
		}

		// Skip ignored directories
		if d.IsDir() {
			if s.classifier.ShouldIgnoreDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// Classify file and apply ignore rules in one pass
		mediaType, shouldProcess, shouldIgnore := s.classifier.ClassifyName(d.Name())
		switch {
		case shouldIgnore:
			s.log.Warn("Ignoring file", "inputPath", absRoot, "walkingPath", s.path.RelativeToHome(p))
			return nil
		case !shouldProcess:
			s.log.Warn("Unsupported file type", "walkingPath", s.path.RelativeToHome(p))
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			s.log.Warn("Failed to get file info", "inputPath", absRoot, "walkingPath", s.path.RelativeToHome(p), "error", err)
			return nil
		}

		file := FileDiscovery{
			Dir:        path.ToSourcePath(filepath.Dir(p)),
			Name:       d.Name(),
			Size:       info.Size(),
			ModTime:    info.ModTime(),
			Extension:  strings.ToLower(filepath.Ext(p)),
			VolumeUUID: volumeUUID,
			MediaType:  mediaType,
		}

		// StreamKey: feeds the TUI progress line, stripped from the plain console.
		s.log.Info("Scanning", logger.StreamKey, true, "file", s.path.RelativeToHome(p))

		// Send to processing channel
		select {
		case output <- file:
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %q: %w", absRoot, err)
	}
	return nil
}

// sweep hard-deletes rows under root not re-seen this scan (last_seen_at
// still older than scanStartedAt), plan and metadata rows included. Only
// called for roots whose walk finished cleanly, so a transient failure
// elsewhere heals on the next clean scan instead of losing rows. No grace
// window: a placed file's row was already repointed at a library-relative
// path by execute, which is never under a scan root, so it is never a sweep
// candidate — nothing else needs the retention a soft delete used to buy.
func (s *Scanner) sweep(ctx context.Context, scanStartedAt time.Time, root string) error {
	// Range match on (file_dir, file_name) avoids a full table scan and
	// needs no LIKE escaping for roots containing % or _. file_dir is stored
	// through path.ToSourcePath (separator only, never the bytes of a name),
	// so root must be run through the same conversion to compare. Trim the
	// trailing separator first, or the filesystem root's range becomes
	// ["//", "/0"), which no file_dir ever falls into.
	trimmed := strings.TrimSuffix(path.ToSourcePath(root), "/")
	prefix := trimmed + "/"
	prefixEnd := trimmed + string(rune('/'+1))
	cutoff := db.FormatTime(scanStartedAt)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sweep %q: begin tx: %w", root, err)
	}
	defer tx.Rollback()

	// The registry row alone: metadata, plan and error rows go with it, by
	// ON DELETE CASCADE
	result, err := tx.ExecContext(ctx,
		`DELETE FROM file_registry WHERE last_seen_at < ? AND (file_dir = ? OR (file_dir >= ? AND file_dir < ?))`,
		cutoff, trimmed, prefix, prefixEnd)
	if err != nil {
		return fmt.Errorf("sweep %q: %w", root, err)
	}
	swept, _ := result.RowsAffected()

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sweep %q: commit: %w", root, err)
	}

	if swept > 0 {
		s.log.Info("Removed vanished files", "path", root, "filesRemoved", swept)
	}
	return nil
}

// store drains the discovery channel and enqueues each file to the BulkWriter
func (s *Scanner) store(ctx context.Context, discoveries <-chan FileDiscovery, storedFiles chan<- FileDiscovery, force bool) {
	var dbWritesWG sync.WaitGroup

	defer func() {
		dbWritesWG.Wait()
		close(storedFiles)
	}()

	for file := range discoveries {
		dbWritesWG.Add(1)
		operation := s.storeScan(ctx, &dbWritesWG, storedFiles, file, force)
		enqueued := s.db.Writer.Write(operation)
		if !enqueued {
			dbWritesWG.Done()
			s.log.Warn("Bulk writer closed; dropping discovery write", "path", file.Name)
		}
	}
}

// storeScan builds the DB callback consumed by BulkWriter.Write. The
// db.DBOperation shape (func(ctx, tx) error) has no return value, so fileID
// is written into the local file copy and sent downstream during execution.
func (s *Scanner) storeScan(ctx context.Context, dbWritesWG *sync.WaitGroup, storedFiles chan<- FileDiscovery, file FileDiscovery, force bool) db.DBOperation {
	// A file whose size or mtime moved (or any file under force) is not the
	// file that was read: its hash, tags and planned folder describe something
	// else. Delete the row — metadata, plan and error rows cascade — and let
	// the insert below make a fresh one. An unchanged file only gets seen.
	const replaceChanged = `
		DELETE FROM file_registry
		WHERE file_dir = ? AND file_name = ?
		  AND (file_size != ? OR file_modified_at != ? OR ? = 1)`
	const query = `
		INSERT INTO file_registry (
			file_dir, file_name, file_size, file_modified_at,
			volume_uuid, media_type, file_extension,
			file_origin,
			discovered_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (file_dir, file_name) DO UPDATE SET
			last_seen_at = excluded.last_seen_at,
			file_origin = excluded.file_origin,
			volume_uuid = COALESCE(excluded.volume_uuid, file_registry.volume_uuid)
		RETURNING id`

	forceInt := 0
	if force {
		forceInt = 1
	}

	queryFileState := func(dbCtx context.Context, tx *sqlx.Tx) (int64, error) {
		var fileID int64
		now := db.FormatTime(time.Now())
		modifiedAt := db.FormatTime(file.ModTime)
		if _, err := tx.ExecContext(dbCtx, replaceChanged,
			file.Dir, file.Name, file.Size, modifiedAt, forceInt); err != nil {
			return 0, err
		}
		err := tx.QueryRowContext(
			dbCtx,
			query,
			file.Dir,
			file.Name,
			file.Size,
			modifiedAt,
			db.StrOrNil(file.VolumeUUID),
			file.MediaType,
			file.Extension,
			FileOriginSource,
			now,
			now,
		).Scan(&fileID)
		return fileID, err
	}
	execute := func(dbCtx context.Context, tx *sqlx.Tx) error {
		defer dbWritesWG.Done()

		select {
		case <-dbCtx.Done():
			return dbCtx.Err()
		default:
		}

		fileID, err := queryFileState(dbCtx, tx)
		if err != nil {
			s.log.Warn("Failed to upsert file", "path", file.Name, "error", err)
			return nil // Continue processing other files in batch
		}

		file.ID = fileID

		// The row is already written at this point — forwarding it downstream
		// is best-effort, not part of "did the DB write succeed". Returning
		// ctx.Err() here made a cancelled pipeline look like a DB failure:
		// BulkWriter would roll back and retry the op, so the file would be
		// written and reported to storedFiles a second time on retry, on top
		// of a Done() double-fire on the already-invoked WaitGroup entry.
		select {
		case storedFiles <- file:
		case <-ctx.Done():
		}

		return nil
	}
	return execute
}
