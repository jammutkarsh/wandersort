package scanner

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

// Scanner handles file discovery and registry population
// It is stateless with respect to individual scan runs; all mutable state
// lives in scanState, which is created fresh for every call to runScan
// This makes concurrent scans safe without any locking on Scanner itself
type Scanner struct {
	db         *db.DB
	classifier *classifier.FileClassifier
	log        logger.Logger
	path       *path.Resolver
	workers    int
}

func New(db *db.DB, log logger.Logger, workers int) *Scanner {
	return &Scanner{
		db:         db,
		classifier: classifier.NewFileClassifier(),
		log:        log,
		path:       path.New(),
		workers:    workers,
	}
}

// Run orchestrates concurrent directory scans across all paths
// It returns the total number of files discovered (new + previously seen) and
// any first error encountered
func (s *Scanner) Run(ctx context.Context, sessionID uuid.UUID, paths []string) (int, error) {
	s.log.Info("Scanner Phase: Processing all paths", "sessionId", sessionID, "pathCount", len(paths))

	type scanResult struct {
		path  string
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

	var newFiles atomic.Int64
	var errorCount atomic.Int64

	// Spawn workers to drain the job queue concurrently
	var workers sync.WaitGroup
	for range s.workers {
		workers.Go(func() {
			for path := range jobs {
				discoveredChan, walkErr := s.scan(ctx, sessionID, path, &newFiles, &errorCount)

				count := 0
				// Drain the channel to both count stored discoveries and wait until
				// scan/store has fully finished this path
				for range discoveredChan {
					count++
				}

				if err := <-walkErr; err != nil {
					s.log.Error("Failed to scan path", "sessionId", sessionID, "path", path, "error", err)
					errorCount.Add(1)
					results <- scanResult{path: path, count: count, err: fmt.Errorf("scan failed for %s: %w", path, err)}
					continue
				}

				s.log.Info("Scanned path", "sessionId", sessionID, "path", path, "filesDiscovered", count)
				results <- scanResult{path: path, count: count}
			}
		})
	}

	// Close results only after all workers are done writing to it
	workers.Wait()
	close(results)

	// Flush before sweeping: dbWritesWG tracks upsert execution inside the
	// batch transaction, not its commit, so only a writer flush guarantees the
	// sweep's own transaction sees every scan_session_id reassignment
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
		if err := s.sweep(ctx, sessionID, result.path); err != nil {
			s.log.Error("Failed to sweep path", "sessionId", sessionID, "path", result.path, "error", err)
			errorCount.Add(1)
			if firstScanErr == nil {
				firstScanErr = err
			}
		}
	}

	newCount := newFiles.Load()
	modifiedCount := int64(totalFiles) - newCount
	errorTotal := errorCount.Load()
	if _, upErr := s.db.ExecContext(ctx, `
		UPDATE scan_sessions
		SET files_discovered = ?, files_new = ?, files_modified = ?, errors_encountered = ?
		WHERE id = ?
	`, totalFiles, newCount, modifiedCount, errorTotal, sessionID.String()); upErr != nil {
		s.log.Error("Failed to update scan counters", "sessionId", sessionID, "error", upErr)
	}

	return totalFiles, firstScanErr
}

// scan executes a scan for a single directory path and returns a channel of
// discovered files plus a single-shot channel carrying the walk's final error.
// It's meant to be called by worker goroutines asynchronously; the error
// channel resolves once the discovery channel is drained
func (s *Scanner) scan(ctx context.Context, sessionID uuid.UUID, path string, newFiles, errorCount *atomic.Int64) (<-chan FileDiscovery, <-chan error) {
	s.log.Info("Scanning path", "sessionId", sessionID, "path", path)
	// Channel for discovered files
	fileDiscoveryChannel := make(chan FileDiscovery, 2*s.workers)
	scanResultsChannel := make(chan FileDiscovery, 2*s.workers)
	walkErr := make(chan error, 1)

	// Start a goroutine to walk the directory and send discoveries to the channel
	// We use a separate channel for walking results to decouple file discovery from database writes

	// Producer
	go func() {
		defer close(scanResultsChannel)

		err := s.walkRoot(ctx, sessionID, path, scanResultsChannel)
		if err != nil {
			s.log.Error("Walk root failed", "sessionId", sessionID, "path", path, "error", err)
		}
		walkErr <- err
	}()

	// Consumer
	go func() {
		s.store(ctx, sessionID, scanResultsChannel, fileDiscoveryChannel, newFiles, errorCount)
	}()

	return fileDiscoveryChannel, walkErr
}

// walkRoot walks absPath and emits FileDiscovery records with relative paths
// absPath is the absolute filesystem path
func (s *Scanner) walkRoot(ctx context.Context, sessionID uuid.UUID, path string, output chan<- FileDiscovery) error {
	absRoot, err := s.path.RealPath(path)
	if err != nil {
		return fmt.Errorf("realpath %q: %w", path, err)
	}

	err = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
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
			s.log.Error("Walk error", "sessionId", sessionID, "inputPath", path, "walkingPath", s.path.RelativeToHome(p), "error", err)
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
			s.log.Warn("Ignoring file", "sessionId", sessionID, "inputPath", path, "walkingPath", s.path.RelativeToHome(p))
			return nil
		case !shouldProcess:
			s.log.Warn("Unsupported file type", "sessionId", sessionID, "walkingPath", s.path.RelativeToHome(p))
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			s.log.Warn("Failed to get file info", "sessionId", sessionID, "inputPath", path, "walkingPath", s.path.RelativeToHome(p), "error", err)
			return nil
		}

		// absRoot = /home/user/pics
		// p = /home/user/pics/2024/sunset.jpg
		// relativeToSource = 2024/sunset.jpg
		relativeToSource, err := filepath.Rel(absRoot, p)
		if err != nil {
			s.log.Warn("Failed to make path relative", "sessionId", sessionID, "inputPath", path, "walkingPath", s.path.RelativeToHome(p), "error", err)
			return nil
		}

		// Persist file path relative to source root for portability
		file := FileDiscovery{
			Path:       relativeToSource,
			Size:       info.Size(),
			ModTime:    info.ModTime(),
			Extension:  strings.ToLower(filepath.Ext(p)),
			SourceRoot: path,
			MediaType:  mediaType,
		}

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

// sweep deletes rows for files under root that this session did not re-see.
// The upsert reassigns scan_session_id on every live file, so any row still
// pointing at an older session was deleted or moved on disk. Timestamps are
// deliberately not compared (last_seen_at is datetime('now'), session
// started_at is RFC3339 — string comparison across the two is broken).
// Runs only for roots whose walk finished cleanly, so an unplugged drive or
// unreadable root never wipes its index.
// ponytail: walkRoot swallows per-file errors, so an unreadable subtree is
// swept here and re-indexed on the next clean scan; the VFS never touches
// disk, so nothing outside the database is at risk
func (s *Scanner) sweep(ctx context.Context, sessionID uuid.UUID, root string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sweep %q: begin tx: %w", root, err)
	}
	defer tx.Rollback()

	// Delete order is forced by foreign keys: virtual_fs_entries.file_id has
	// no ON DELETE action, and file_metadata's SET NULL would leave orphan
	// rows that still participate in the scorer's hash grouping
	statements := []string{
		`DELETE FROM virtual_fs_entries WHERE file_id IN
			(SELECT id FROM file_registry WHERE source_root = ? AND scan_session_id != ?)`,
		`DELETE FROM file_metadata WHERE file_id IN
			(SELECT id FROM file_registry WHERE source_root = ? AND scan_session_id != ?)`,
		`DELETE FROM file_registry WHERE source_root = ? AND scan_session_id != ?`,
	}
	var swept int64
	for _, stmt := range statements {
		result, err := tx.ExecContext(ctx, stmt, root, sessionID.String())
		if err != nil {
			return fmt.Errorf("sweep %q: %w", root, err)
		}
		swept, _ = result.RowsAffected()
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sweep %q: commit: %w", root, err)
	}

	if swept > 0 {
		s.log.Info("Swept vanished files", "sessionId", sessionID, "path", root, "filesRemoved", swept)
	}
	return nil
}

// store drains the discovery channel and enqueues each file to the BulkWriter
func (s *Scanner) store(ctx context.Context, sessionID uuid.UUID, discoveries <-chan FileDiscovery, storedFiles chan<- FileDiscovery, newFiles, errorCount *atomic.Int64) {
	var dbWritesWG sync.WaitGroup

	defer func() {
		dbWritesWG.Wait()
		close(storedFiles)
	}()

	for file := range discoveries {
		dbWritesWG.Add(1)
		operation := s.storeScan(ctx, sessionID, &dbWritesWG, storedFiles, file, newFiles, errorCount)
		enqueued := s.db.Writer.Write(operation)
		if !enqueued {
			dbWritesWG.Done()
			s.log.Warn("Bulk writer closed; dropping discovery write", "path", file.Path, "sessionId", sessionID)
		}
	}
}

// storeScan builds the DB callback consumed by BulkWriter.Write
//
// The callback shape is fixed by db.DBOperation (func(ctx, tx) error), so we
// cannot return fileID directly to the caller of Write at enqueue time
// Instead, we compute (id, isNew) during execution and mutate the local file
// copy before sending it downstream
func (s *Scanner) storeScan(ctx context.Context, sessionID uuid.UUID, dbWritesWG *sync.WaitGroup, storedFiles chan<- FileDiscovery, file FileDiscovery, newFiles, errorCount *atomic.Int64) db.DBOperation {
	const query = `
		INSERT INTO file_registry (
			file_path, file_size, file_modified_at,
			scan_session_id, source_root, media_type, file_extension,
			scan_status, path_type, file_origin,
			discovered_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		ON CONFLICT (file_path, source_root) DO UPDATE SET
			last_seen_at = datetime('now'),
			scan_session_id = excluded.scan_session_id,
			file_origin = excluded.file_origin,
			file_size = excluded.file_size,
			file_modified_at = excluded.file_modified_at,
			scan_status = CASE WHEN file_registry.file_size != excluded.file_size
					OR file_registry.file_modified_at != excluded.file_modified_at
					OR file_registry.scan_status IN ('HASHING','ERROR')
				THEN 'DISCOVERED' ELSE file_registry.scan_status END
		RETURNING id, (discovered_at = last_seen_at) AS is_new`

	queryFileState := func(dbCtx context.Context, tx *sqlx.Tx) (int64, int, error) {
		var fileID int64
		var isNew int // SQLite does not have a real BOOLEAN storage class
		err := tx.QueryRowContext(
			dbCtx,
			query,
			file.Path,
			file.Size,
			file.ModTime.Format(time.RFC3339),
			sessionID.String(),
			file.SourceRoot,
			file.MediaType,
			file.Extension,
			db.StatusDiscovered,
			PathTypeRelative,
			FileOriginSource,
		).Scan(&fileID, &isNew)
		return fileID, isNew, err
	}
	execute := func(dbCtx context.Context, tx *sqlx.Tx) error {
		defer dbWritesWG.Done()

		select {
		case <-dbCtx.Done():
			return dbCtx.Err()
		default:
		}

		fileID, isNew, err := queryFileState(dbCtx, tx)
		if err != nil {
			s.log.Warn("Failed to upsert file", "sessionId", sessionID, "path", file.Path, "error", err)
			errorCount.Add(1)
			return nil // Continue processing other files in batch
		}

		if isNew == 1 {
			newFiles.Add(1)
		}

		file.ID = fileID

		select {
		case storedFiles <- file:
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	}
	return execute
}
