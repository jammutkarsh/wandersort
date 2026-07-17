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
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// deletedRetention is the grace window before a vanished file's rows are
// hard-purged: long enough to survive an unplugged drive or a temporarily
// unreadable subtree, short enough that the index doesn't hoard ghosts
const deletedRetention = 30 * 24 * time.Hour

// Scanner handles file discovery and registry population
// It is stateless with respect to individual scan runs; all mutable state
// lives in scanState, which is created fresh for every call to runScan
// This makes concurrent scans safe without any locking on Scanner itself
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

// Run orchestrates concurrent directory scans across all paths
// It returns the total number of files discovered (new + previously seen) and
// any first error encountered
func (s *Scanner) Run(ctx context.Context, sessionID uuid.UUID, paths []string) (int, error) {
	s.log.Info("Scanner Phase: Processing all paths", "sessionId", sessionID, "pathCount", len(paths))

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

	var newFiles atomic.Int64
	var errorCount atomic.Int64

	// Spawn workers to drain the job queue concurrently
	var workers sync.WaitGroup
	for range s.workers {
		workers.Go(func() {
			for path := range jobs {
				// Canonicalize once; every stored file_dir and the sweep must
				// agree on the same absolute root spelling
				absRoot, err := s.path.RealPath(path)
				if err != nil {
					s.log.Error("Failed to resolve path", "sessionId", sessionID, "path", path, "error", err)
					errorCount.Add(1)
					results <- scanResult{err: fmt.Errorf("resolve %s: %w", path, err)}
					continue
				}

				volumeUUID := s.volumes.ForPath(absRoot)
				discoveredChan, walkErr := s.scan(ctx, sessionID, absRoot, volumeUUID, &newFiles, &errorCount)

				count := 0
				// Drain the channel to both count stored discoveries and wait until
				// scan/store has fully finished this path
				for range discoveredChan {
					count++
				}

				if err := <-walkErr; err != nil {
					s.log.Error("Failed to scan path", "sessionId", sessionID, "path", absRoot, "error", err)
					errorCount.Add(1)
					results <- scanResult{root: absRoot, count: count, err: fmt.Errorf("scan failed for %s: %w", path, err)}
					continue
				}

				s.log.Info("Scanned path", "sessionId", sessionID, "path", absRoot, "filesDiscovered", count)
				results <- scanResult{root: absRoot, count: count}
			}
		})
	}

	// Close results only after all workers are done writing to it
	workers.Wait()
	close(results)

	// Flush before sweeping: dbWritesWG tracks upsert execution inside the
	// batch transaction, not its commit, so only a writer flush guarantees the
	// sweep's own statement sees every scan_session_id reassignment
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
		if err := s.sweep(ctx, sessionID, result.root); err != nil {
			s.log.Error("Failed to sweep path", "sessionId", sessionID, "path", result.root, "error", err)
			errorCount.Add(1)
			if firstScanErr == nil {
				firstScanErr = err
			}
		}
	}

	// GC failure keeps ghosts a little longer; never fail the scan over it
	if err := s.purgeExpired(ctx, sessionID); err != nil {
		s.log.Error("Failed to purge expired files", "sessionId", sessionID, "error", err)
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

// scan executes a scan for a single directory root and returns a channel of
// discovered files plus a single-shot channel carrying the walk's final error.
// It's meant to be called by worker goroutines asynchronously; the error
// channel resolves once the discovery channel is drained
func (s *Scanner) scan(ctx context.Context, sessionID uuid.UUID, absRoot, volumeUUID string, newFiles, errorCount *atomic.Int64) (<-chan FileDiscovery, <-chan error) {
	s.log.Info("Scanning path", "sessionId", sessionID, "path", absRoot)
	// Channel for discovered files
	fileDiscoveryChannel := make(chan FileDiscovery, 2*s.workers)
	scanResultsChannel := make(chan FileDiscovery, 2*s.workers)
	walkErr := make(chan error, 1)

	// Start a goroutine to walk the directory and send discoveries to the channel
	// We use a separate channel for walking results to decouple file discovery from database writes

	// Producer
	go func() {
		defer close(scanResultsChannel)

		err := s.walkRoot(ctx, sessionID, absRoot, volumeUUID, scanResultsChannel)
		if err != nil {
			s.log.Error("Walk root failed", "sessionId", sessionID, "path", absRoot, "error", err)
		}
		walkErr <- err
	}()

	// Consumer
	go func() {
		s.store(ctx, sessionID, scanResultsChannel, fileDiscoveryChannel, newFiles, errorCount)
	}()

	return fileDiscoveryChannel, walkErr
}

// walkRoot walks absRoot (already canonical) and emits FileDiscovery records
// carrying the file's absolute directory and name
func (s *Scanner) walkRoot(ctx context.Context, sessionID uuid.UUID, absRoot, volumeUUID string, output chan<- FileDiscovery) error {
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
			s.log.Error("Walk error", "sessionId", sessionID, "inputPath", absRoot, "walkingPath", s.path.RelativeToHome(p), "error", err)
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
			s.log.Warn("Ignoring file", "sessionId", sessionID, "inputPath", absRoot, "walkingPath", s.path.RelativeToHome(p))
			return nil
		case !shouldProcess:
			s.log.Warn("Unsupported file type", "sessionId", sessionID, "walkingPath", s.path.RelativeToHome(p))
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			s.log.Warn("Failed to get file info", "sessionId", sessionID, "inputPath", absRoot, "walkingPath", s.path.RelativeToHome(p), "error", err)
			return nil
		}

		file := FileDiscovery{
			Dir:        filepath.Dir(p),
			Name:       d.Name(),
			Size:       info.Size(),
			ModTime:    info.ModTime(),
			Extension:  strings.ToLower(filepath.Ext(p)),
			VolumeUUID: volumeUUID,
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

// sweep soft-deletes rows for files under root that this session did not
// re-see. The upsert reassigns scan_session_id (and clears deleted_at) on
// every live file, so any live row still pointing at an older session was
// deleted or moved on disk. Rows only get stamped, never removed here —
// purgeExpired hard-deletes them after the retention window, so a transient
// failure (unreadable subtree, one bad upsert) heals on the next clean scan.
// Runs only for roots whose walk finished cleanly
func (s *Scanner) sweep(ctx context.Context, sessionID uuid.UUID, root string) error {
	// Prefix match as an index range on (file_dir, file_name):
	// [root+sep, root+succ(sep)) covers every path under root without a full
	// table scan, and needs no LIKE escaping for roots containing % or _.
	// Trim a trailing separator first: for the filesystem root the range
	// would otherwise be ["//", "/0"), which no file_dir ever falls into
	trimmed := strings.TrimSuffix(root, string(filepath.Separator))
	prefix := trimmed + string(filepath.Separator)
	prefixEnd := trimmed + string(filepath.Separator+1)
	result, err := s.db.ExecContext(ctx, `
		UPDATE file_registry SET deleted_at = ?
		WHERE deleted_at IS NULL
			AND scan_session_id != ?
			AND (file_dir = ? OR (file_dir >= ? AND file_dir < ?))`,
		db.FormatTime(time.Now()), sessionID.String(), trimmed, prefix, prefixEnd)
	if err != nil {
		return fmt.Errorf("sweep %q: %w", root, err)
	}

	if swept, _ := result.RowsAffected(); swept > 0 {
		s.log.Info("Marked vanished files", "sessionId", sessionID, "path", root, "filesRemoved", swept)
	}
	return nil
}

// purgeExpired hard-deletes rows soft-deleted longer than deletedRetention ago
func (s *Scanner) purgeExpired(ctx context.Context, sessionID uuid.UUID) error {
	cutoff := db.FormatTime(time.Now().Add(-deletedRetention))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("purge expired: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Delete order is forced by foreign keys: virtual_fs_entries.file_id has
	// no ON DELETE action, and file_metadata's SET NULL would leave orphan
	// rows that still participate in the scorer's hash grouping
	statements := []string{
		`DELETE FROM virtual_fs_entries WHERE file_id IN
			(SELECT id FROM file_registry WHERE deleted_at < ?)`,
		`DELETE FROM file_metadata WHERE file_id IN
			(SELECT id FROM file_registry WHERE deleted_at < ?)`,
		`DELETE FROM file_registry WHERE deleted_at < ?`,
	}
	var purged int64
	for _, stmt := range statements {
		result, err := tx.ExecContext(ctx, stmt, cutoff)
		if err != nil {
			return fmt.Errorf("purge expired: %w", err)
		}
		purged, _ = result.RowsAffected()
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("purge expired: commit: %w", err)
	}

	if purged > 0 {
		s.log.Info("Purged expired files", "sessionId", sessionID, "filesPurged", purged)
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
			s.log.Warn("Bulk writer closed; dropping discovery write", "path", file.Name, "sessionId", sessionID)
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
			file_dir, file_name, file_size, file_modified_at,
			scan_session_id, volume_uuid, media_type, file_extension,
			scan_status, file_origin,
			discovered_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (file_dir, file_name) DO UPDATE SET
			last_seen_at = excluded.last_seen_at,
			scan_session_id = excluded.scan_session_id,
			file_origin = excluded.file_origin,
			file_size = excluded.file_size,
			file_modified_at = excluded.file_modified_at,
			volume_uuid = COALESCE(excluded.volume_uuid, file_registry.volume_uuid),
			deleted_at = NULL,
			scan_status = CASE WHEN file_registry.file_size != excluded.file_size
					OR file_registry.file_modified_at != excluded.file_modified_at
					OR file_registry.scan_status IN ('HASHING','ERROR')
				THEN 'DISCOVERED' ELSE file_registry.scan_status END
		RETURNING id, (discovered_at = last_seen_at) AS is_new`

	queryFileState := func(dbCtx context.Context, tx *sqlx.Tx) (int64, int, error) {
		var fileID int64
		var isNew int // SQLite does not have a real BOOLEAN storage class
		now := db.FormatTime(time.Now())
		err := tx.QueryRowContext(
			dbCtx,
			query,
			file.Dir,
			file.Name,
			file.Size,
			db.FormatTime(file.ModTime),
			sessionID.String(),
			db.StrOrNil(file.VolumeUUID),
			file.MediaType,
			file.Extension,
			db.StatusDiscovered,
			FileOriginSource,
			now,
			now,
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
			s.log.Warn("Failed to upsert file", "sessionId", sessionID, "path", file.Name, "error", err)
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
