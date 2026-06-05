package scanner

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	sm "github.com/jammutkarsh/wandersort/pkg/statusmanager"
)

// Scanner handles file discovery and registry population.
// It is stateless with respect to individual scan runs; all mutable state
// lives in scanState, which is created fresh for every call to runScan.
// This makes concurrent scans safe without any locking on Scanner itself.
type Scanner struct {
	db         *db.DB
	classifier *classifier.FileClassifier
	log        logger.Logger
	path       *path.Resolver
	tracker    *sm.Tracker
	scanBuffer int // Channel buffer size
}

// NewScanner creates a new scanner instance.
func NewScanner(db *db.DB, log logger.Logger) *Scanner {
	return &Scanner{
		db:         db,
		classifier: classifier.NewFileClassifier(),
		log:        log,
		path:       path.New(),
		scanBuffer: 1000,
	}
}

func (s *Scanner) Run(ctx context.Context, tracker *sm.Tracker, paths []string, workerCount int) (int, error) {
	s.log.Info("Scanner Phase: Processing all paths", "session_id", tracker.SessionID, "path_count", len(paths))

	type scanResult struct {
		count int
		err   error
	}

	// Buffer exactly one result per path; no path produces more than one result.
	results := make(chan scanResult, len(paths))

	// Enqueue all work up front so workers can start immediately without blocking.
	jobs := make(chan string, len(paths))
	for _, path := range paths {
		jobs <- path
	}

	// Accept no more jobs; workers will stop when they drain the queue.
	close(jobs)

	// Spawn workers to drain the job queue concurrently.
	var workers sync.WaitGroup
	for range workerCount {
		workers.Go(func() {
			for path := range jobs {
				discoveredChan, err := s.scan(ctx, path)
				if err != nil {
					s.log.Error("Failed to scan path", "session_id", tracker.SessionID, "path", path, "error", err)
					results <- scanResult{err: fmt.Errorf("scan failed for %s: %w", path, err)}
					continue
				}

				count := 0
				// Drain the channel to both count stored discoveries and wait until
				// scan/store has fully finished this path.
				for range discoveredChan {
					count++
				}

				s.log.Info("Scanned path", "session_id", tracker.SessionID, "path", path, "files_discovered", count)
				results <- scanResult{count: count}
			}
		})
	}

	// Close results only after all workers are done writing to it.
	workers.Wait()
	close(results)

	totalFiles := 0
	var firstScanErr error
	for result := range results {
		totalFiles += result.count
		if result.err != nil && firstScanErr == nil {
			firstScanErr = result.err
		}
	}

	return totalFiles, firstScanErr
}

// scan executes a scan for a single directory path and returns a channel
// of discovered files. It's meant to be called by worker goroutines asynchronously.
func (s *Scanner) scan(ctx context.Context, path string) (<-chan FileDiscovery, error) {
	// Channel for discovered files
	fileDiscoveryChannel := make(chan FileDiscovery, s.scanBuffer) // In
	scanResultsChannel := make(chan FileDiscovery, s.scanBuffer)   // Out

	// Start a goroutine to walk the directory and send discoveries to the channel.
	// We use a separate channel for walking results to decouple file discovery from database writes.

	// Producer
	go func() {
		defer close(scanResultsChannel)

		if err := s.walkRoot(ctx, path, scanResultsChannel); err != nil {
			s.log.Error("Walk root failed during scanPath", "path", path, "error", err)
		}
	}()

	// Consumer
	go func() {
		s.store(ctx, s.tracker.SessionID, scanResultsChannel, fileDiscoveryChannel)
	}()

	return fileDiscoveryChannel, nil
}

// walkRoot walks absPath and emits FileDiscovery records with relative paths.
// absPath is the absolute filesystem path.
func (s *Scanner) walkRoot(ctx context.Context, path string, output chan<- FileDiscovery) error {
	absRoot, err := s.path.RealPath(path)
	if err != nil {
		s.log.Error("Failed to resolve root path", "input_path", path, "error", err)
		return err
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
			s.log.Error("Walk error", "input_path", path, "walking_path", s.path.RelativeToHome(p), "error", err)
			s.tracker.Errors.Add(1)
			return nil // Continue walking
		}

		// Skip ignored directories
		if d.IsDir() {
			if s.classifier.ShouldIgnoreDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// Classify file and apply ignore rules in one pass.
		mediaType, shouldProcess, shouldIgnore := s.classifier.ClassifyName(d.Name())
		switch {
		case shouldIgnore:
			s.log.Debug("Ignoring file", "input_path", path, "walking_path", s.path.RelativeToHome(p))
			s.tracker.Skipped.Add(1)
			return nil
		case !shouldProcess:
			s.log.Debug("Unsupported file type", "input_path", path, "walking_path", s.path.RelativeToHome(p))
			s.tracker.AddUnsupportedPath(s.path.RelativeToHome(p))
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			s.log.Warn("Failed to get file info", "input_path", path, "walking_path", s.path.RelativeToHome(p), "error", err)
			s.tracker.Errors.Add(1)
			return nil
		}

		// absRoot = /home/user/pics
		// p = /home/user/pics/2024/sunset.jpg
		// relativeToSource = 2024/sunset.jpg
		relativeToSource, err := filepath.Rel(absRoot, p)
		if err != nil {
			s.log.Warn("Failed to make path relative", "input_path", path, "walking_path", s.path.RelativeToHome(p), "error", err)
			s.tracker.Errors.Add(1)
			return nil
		}

		// Persist file path relative to source root for portability.
		capture := deriveCapture(d.Name(), strings.ToLower(filepath.Ext(p)), mediaType)
		file := FileDiscovery{
			Path:       relativeToSource,
			Size:       info.Size(),
			ModTime:    info.ModTime(),
			Extension:  strings.ToLower(filepath.Ext(p)),
			SourceRoot: path,
			MediaType:  mediaType,
			Capture:    capture,
		}

		s.tracker.Discovered.Add(1)

		// Send to processing channel
		select {
		case output <- file:
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	})

	if err != nil {
		s.log.Error("Walk failed", "input_path", path, "error", err)
		return err
	}
	return nil
}

func (s *Scanner) store(ctx context.Context, sessionID uuid.UUID, discoveries <-chan FileDiscovery, storedFiles chan<- FileDiscovery) {
	var dbWritesWG sync.WaitGroup
	var emittersWG sync.WaitGroup
	emitQueue := make(chan FileDiscovery, s.scanBuffer)

	// TODO: This shouldn't be hardcoded value
	for range 4 { // Emitters
		emittersWG.Go(func() {
			for file := range emitQueue {
				select {
				case storedFiles <- file:
				case <-ctx.Done():
					return
				}
			}
		})
	}

	defer func() {
		// 1) Wait for all queued DB write callbacks to finish.
		// 2) Close emit queue so emit workers can drain and exit.
		// 3) Wait for emitters, then close final output channel.
		dbWritesWG.Wait()
		close(emitQueue)
		emittersWG.Wait()
		close(storedFiles)
	}()

	for file := range discoveries {
		dbWritesWG.Add(1)
		operation := s.storeScan(ctx, sessionID, &dbWritesWG, emitQueue, file)
		enqueued := s.db.Writer.Write(operation)
		if !enqueued {
			dbWritesWG.Done()
			s.tracker.Errors.Add(1)
			s.log.Warn("Bulk writer closed; dropping discovery write", "path", file.Path, "session_id", sessionID)
		}
	}
}

// storeScan builds the DB callback consumed by BulkWriter.Write.
//
// Why these captured values are part of this closure:
//   - ctx: needed to stop emit handoff promptly when the scan is canceled.
//   - emitQueue: this callback can only emit after the DB write succeeds.
//   - file.ID: ID is produced by RETURNING at actual DB execution time, so it
//     must be assigned inside this callback before forwarding the file.
//
// The callback shape is fixed by db.DBOperation (func(ctx, tx) error), so we
// cannot return fileID directly to the caller of Write at enqueue time.
// Instead, we compute (id, isNew) during execution and mutate the local file
// copy before emitting it downstream.
func (s *Scanner) storeScan(ctx context.Context, sessionID uuid.UUID, dbWritesWG *sync.WaitGroup, emitQueue chan<- FileDiscovery, file FileDiscovery) db.DBOperation {
	const query = `
		INSERT INTO file_registry (
			file_path, file_size, file_modified_at,
			scan_session_id, source_root, media_type, file_extension,
			scan_status, path_type, file_origin, capture_stem, capture_role,
			discovered_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		ON CONFLICT (file_path, source_root) DO UPDATE SET
			last_seen_at = datetime('now'),
			scan_session_id = excluded.scan_session_id,
			file_origin = excluded.file_origin,
			file_size = excluded.file_size,
			file_modified_at = excluded.file_modified_at,
			capture_stem = excluded.capture_stem,
			capture_role = excluded.capture_role,
			updated_at = datetime('now')
		RETURNING id, (discovered_at = last_seen_at) AS is_new`

	queryFileState := func(dbCtx context.Context, tx *sql.Tx) (int64, int, error) {
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
			ScanStatusDiscovered,
			PathTypeRelative,
			FileOriginSource,
			file.Capture.captureKey,
			file.Capture.variant,
		).Scan(&fileID, &isNew)
		return fileID, isNew, err
	}
	execute := func(dbCtx context.Context, tx *sql.Tx) error {
		defer dbWritesWG.Done()

		select {
		case <-dbCtx.Done():
			return dbCtx.Err()
		default:
		}

		fileID, isNew, err := queryFileState(dbCtx, tx)
		if err != nil {
			s.log.Warn("Failed to upsert file", "path", file.Path, "error", err)
			s.tracker.Errors.Add(1)
			return nil // Continue processing other files in batch.
		}

		file.ID = fileID

		if isNew == 1 {
			s.tracker.NewFiles.Add(1)
		} else {
			s.tracker.Skipped.Add(1)
		}

		select {
		case emitQueue <- file:
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	}
	return execute
}
