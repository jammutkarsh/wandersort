package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jammutkarsh/wandersort/pkg/core/classifier"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/queue"
	"github.com/riverqueue/river"
)

// Scanner handles file discovery and registry population.
// It is stateless with respect to individual scan runs; all mutable state
// lives in scanState, which is created fresh for every call to runScan.
// This makes concurrent scans safe without any locking on Scanner itself.
type Scanner struct {
	db         *pgxpool.Pool
	classifier *classifier.FileClassifier
	log        logger.Logger
	config     ScanConfig
	pathUtil   *PathUtil
	outputPath string
	jobQueue   queue.Enqueuer
}

// scanState holds all mutable counters and collections for a single scan run.
// A fresh instance is allocated by runScan, so concurrent scans never share
// any counter or slice — no data races, no cross-scan leakage.
type scanState struct {
	discovered int64
	skipped    int64
	newFiles   int64
	modified   int64
	errors     int64

	unsupported      int64
	unsupportedMu    sync.Mutex
	unsupportedPaths []string
}

// ScanConfig holds scanner configuration
type ScanConfig struct {
	// Concurrency
	MaxWalkers       int           // Number of parallel directory walkers
	WorkerBufferSize int           // Channel buffer size
	BatchInsertSize  int           // How many files to insert at once
	ProgressInterval time.Duration // How often to update DB with progress
}

// NewScanner creates a new scanner instance.
// The enqueuer is injected later via SetEnqueuer (see pkg/queue).
func NewScanner(db *pgxpool.Pool, log logger.Logger, outputPath string) *Scanner {
	pathUtil, err := NewPathUtil()
	if err != nil {
		log.Error("Failed to initialize path utility", "error", err)
		// Fallback: use HOME env var
		homeDir, _ := os.UserHomeDir()
		pathUtil = &PathUtil{homeDir: homeDir}
	}

	return &Scanner{
		db:         db,
		classifier: classifier.NewFileClassifier(),
		log:        log,
		pathUtil:   pathUtil,
		outputPath: outputPath,
		config: ScanConfig{
			MaxWalkers:       4,
			WorkerBufferSize: 1000,
			BatchInsertSize:  500,
			ProgressInterval: 5 * time.Second,
		},
	}
}

// SetEnqueuer injects the Enqueuer after construction.
// Called by queue.New so the scanner can dispatch jobs without importing the queue package.
func (s *Scanner) SetEnqueuer(e queue.Enqueuer) {
	s.jobQueue = e
}

// StartScan validates rootPaths, creates the scan_sessions DB row, enqueues a
// River job, and returns the new sessionID so the caller can poll for status.
func (s *Scanner) StartScan(ctx context.Context, rootPaths []string) (uuid.UUID, error) {
	sessionID, expandedRoots, err := s.prepareSession(ctx, rootPaths)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.jobQueue.Enqueue(ctx, ScanTaskArgs{
		SessionID:     sessionID.String(),
		OriginalPaths: rootPaths,
		ExpandedRoots: expandedRoots,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("failed to enqueue scan job: %w", err)
	}
	return sessionID, nil
}

// prepareSession validates rootPaths, creates the scan_sessions DB row, and
// returns the sessionID and expanded (absolute) root paths.
func (s *Scanner) prepareSession(ctx context.Context, rootPaths []string) (uuid.UUID, []string, error) {
	// Expand and validate root paths
	expandedRoots := make([]string, len(rootPaths))
	for i, root := range rootPaths {
		expanded := s.pathUtil.ExpandPath(root)
		if _, err := os.Stat(expanded); os.IsNotExist(err) {
			return uuid.Nil, nil, fmt.Errorf("root path does not exist: %s (expanded from %s)", expanded, root)
		}
		expandedRoots[i] = expanded
	}

	// Create scan session
	sessionID := uuid.New()
	startedAt := time.Now()

	// Convert root paths to JSON for PostgreSQL JSONB
	rootPathsJSON, err := json.Marshal(rootPaths)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to marshal root paths: %w", err)
	}

	// Insert session into DB
	_, err = s.db.Exec(ctx, `
		INSERT INTO scan_sessions (id, started_at, status, root_paths)
		VALUES ($1, $2, $3, $4)
	`, sessionID, startedAt, ScanStatusRunning, rootPathsJSON)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to create scan session: %w", err)
	}

	s.log.Info("Scan session created", "session_id", sessionID, "root_paths", rootPaths)
	return sessionID, expandedRoots, nil
}

// executeScan is the core scan pipeline. It is called by ScanTaskWorker.Work
// and runs entirely within River's worker goroutine — no extra goroutine needed.
func (s *Scanner) executeScan(ctx context.Context, session *ScanSession, expandedRoots []string) {
	// Allocate fresh state for this run — no shared mutable fields on Scanner.
	st := &scanState{}

	// Channel for discovered files
	filesChan := make(chan FileDiscovery, s.config.WorkerBufferSize)

	// Start progress updater
	progressCtx, cancelProgress := context.WithCancel(ctx)
	defer cancelProgress()
	go s.updateProgress(progressCtx, session.ID, st)

	// Collect first walk error across all goroutines
	var (
		walkErrMu sync.Mutex
		walkErr   error
	)

	// Start walker goroutines for each root path (expanded + original pairs)
	var wg sync.WaitGroup
	for i, expandedRoot := range expandedRoots {
		wg.Add(1)
		go func(expanded, original string) {
			defer wg.Done()
			if err := s.walkRoot(ctx, expanded, original, filesChan, st); err != nil {
				walkErrMu.Lock()
				if walkErr == nil {
					walkErr = err
				}
				walkErrMu.Unlock()
			}
		}(expandedRoot, session.RootPaths[i])
	}

	// Close channel when all walkers complete
	go func() {
		wg.Wait()
		close(filesChan)
	}()

	// Process discovered files in batches (returns after channel is closed)
	s.processDiscoveries(ctx, session.ID, filesChan, st)

	// Write unsupported files report (walkers are all done at this point)
	s.writeUnsupportedFiles(session.ID, st)

	// Skip cleanup when context is cancelled — a partial scan would incorrectly
	// delete registry entries for files that simply weren't visited yet.
	var cleanupErr error
	if ctx.Err() == nil {
		cleanupErr = s.cleanupDeletedFiles(ctx, session.ID, session.RootPaths)
	}

	// Determine final status from the first significant error encountered.
	walkErrMu.Lock()
	ferr := walkErr
	walkErrMu.Unlock()
	if ferr == nil {
		ferr = cleanupErr
	}

	finalStatus := ScanStatusCompleted
	var lastError *string
	if ferr != nil {
		errStr := ferr.Error()
		lastError = &errStr
		if errors.Is(ferr, context.Canceled) {
			finalStatus = ScanStatusCancelled
		} else {
			finalStatus = ScanStatusFailed
		}
	}

	// Use a detached context so the final status is always persisted, even when
	// the parent context has been cancelled (e.g. during graceful shutdown).
	completeCtx, completeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer completeCancel()
	s.completeScan(completeCtx, session, finalStatus, lastError, st)
}

// walkRoot walks expandedRoot and emits FileDiscovery records with relative paths.
// expandedRoot is the absolute filesystem path; originalRoot is the stored path (may use ~).
func (s *Scanner) walkRoot(ctx context.Context, expandedRoot, originalRoot string, output chan<- FileDiscovery, st *scanState) error {
	s.log.Info("Starting walk", "root", expandedRoot)

	err := filepath.WalkDir(expandedRoot, func(path string, d fs.DirEntry, err error) error {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Handle errors (permission denied, etc.)
		if err != nil {
			s.log.Warn("Walk error", "path", path, "error", err)
			atomic.AddInt64(&st.errors, 1)
			return nil // Continue walking
		}

		// Skip ignored directories
		if d.IsDir() {
			if s.classifier.ShouldIgnoreDir(d.Name()) {
				s.log.Debug("Skipping ignored directory", "path", path)
				return filepath.SkipDir
			}
			return nil
		}

		// Skip ignored files
		if s.classifier.ShouldIgnore(d.Name()) {
			return nil
		}

		// Classify file
		mediaType, shouldProcess := s.classifier.Classify(path)
		if !shouldProcess {
			s.log.Debug("Unsupported file type, skipping", "path", path)
			atomic.AddInt64(&st.unsupported, 1)
			st.unsupportedMu.Lock()
			st.unsupportedPaths = append(st.unsupportedPaths, path)
			st.unsupportedMu.Unlock()
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			s.log.Warn("Failed to get file info", "path", path, "error", err)
			atomic.AddInt64(&st.errors, 1)
			return nil
		}

		// Make path relative to the expanded root
		relPath, err := s.pathUtil.MakeRelative(path, expandedRoot)
		if err != nil {
			s.log.Warn("Failed to make path relative", "path", path, "error", err)
			atomic.AddInt64(&st.errors, 1)
			return nil
		}

		// Create discovery record with relative path
		capture := DeriveCapture(d.Name(), strings.ToLower(filepath.Ext(path)), mediaType)
		discovery := FileDiscovery{
			Path:        relPath, // Relative to source root
			Size:        info.Size(),
			ModTime:     info.ModTime(),
			Extension:   strings.ToLower(filepath.Ext(path)),
			SourceRoot:  originalRoot, // Store original (may contain ~)
			MediaType:   mediaType,
			CaptureStem: capture.Stem,
			CaptureRole: capture.Role,
		}

		atomic.AddInt64(&st.discovered, 1)

		// Send to processing channel
		select {
		case output <- discovery:
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	})

	if err != nil {
		s.log.Error("Walk failed", "root", expandedRoot, "error", err)
		return err
	}
	return nil
}

// writeUnsupportedFiles writes all paths with unsupported extensions that were
// collected during the scan to <outputPath>/unsupported_files_<sessionID>.txt,
// one absolute path per line, sorted alphabetically.
// No file is created when every scanned file had a recognised extension.
func (s *Scanner) writeUnsupportedFiles(sessionID uuid.UUID, st *scanState) {
	st.unsupportedMu.Lock()
	paths := make([]string, len(st.unsupportedPaths))
	copy(paths, st.unsupportedPaths)
	st.unsupportedMu.Unlock()

	if len(paths) == 0 {
		s.log.Debug("No unsupported files found; skipping report")
		return
	}

	sort.Strings(paths)

	if err := os.MkdirAll(s.outputPath, 0o755); err != nil {
		s.log.Error("Failed to create output directory for unsupported report", "error", err)
		return
	}

	reportPath := filepath.Join(s.outputPath, fmt.Sprintf("unsupported_files_%s.txt", sessionID))
	f, err := os.Create(reportPath)
	if err != nil {
		s.log.Error("Failed to create unsupported files report", "path", reportPath, "error", err)
		return
	}
	defer f.Close()

	header := fmt.Sprintf(
		"# Unsupported files found during scan %s\n"+
			"# These file types are not yet supported by WanderSort.\n"+
			"# Please raise a feature request at https://github.com/jammutkarsh/wandersort/issues\n\n",
		sessionID,
	)
	if _, err := fmt.Fprint(f, header); err != nil {
		s.log.Error("Failed to write report header", "error", err)
		return
	}

	for _, p := range paths {
		if _, err := fmt.Fprintln(f, p); err != nil {
			s.log.Error("Failed to write path to report", "path", p, "error", err)
			return
		}
	}

	// Flush to disk to ensure the report survives unexpected termination.
	if err := f.Sync(); err != nil {
		s.log.Error("Failed to sync unsupported files report", "error", err)
	}

	s.log.Info("Unsupported files report written", "path", reportPath, "count", len(paths))
}

func (s *Scanner) processDiscoveries(ctx context.Context, sessionID uuid.UUID, discoveries <-chan FileDiscovery, st *scanState) {
	batch := make([]FileDiscovery, 0, s.config.BatchInsertSize)

	for discovery := range discoveries {
		batch = append(batch, discovery)

		if len(batch) >= s.config.BatchInsertSize {
			s.insertBatch(ctx, sessionID, batch, st)
			batch = batch[:0] // Reset batch
		}
	}

	// Insert remaining files
	if len(batch) > 0 {
		s.insertBatch(ctx, sessionID, batch, st)
	}
}

func (s *Scanner) insertBatch(ctx context.Context, sessionID uuid.UUID, batch []FileDiscovery, st *scanState) {
	if len(batch) == 0 {
		return
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		s.log.Error("Failed to begin transaction", "error", err)
		atomic.AddInt64(&st.errors, int64(len(batch)))
		return
	}
	defer tx.Rollback(ctx)

	// Use pgx batch for better performance
	batchQuery := &pgx.Batch{}

	for _, file := range batch {
		batchQuery.Queue(`
			INSERT INTO file_registry (
				file_path, file_size, file_modified_at,
				scan_session_id, source_root, media_type, file_extension,
				scan_status, path_type, file_origin, capture_stem, capture_role,
				discovered_at, last_seen_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
			ON CONFLICT (file_path, source_root) DO UPDATE SET
				last_seen_at = NOW(),
				scan_session_id = EXCLUDED.scan_session_id,
				file_origin = EXCLUDED.file_origin,
				file_size = EXCLUDED.file_size,
				file_modified_at = EXCLUDED.file_modified_at,
				capture_stem = EXCLUDED.capture_stem,
				capture_role = EXCLUDED.capture_role,
				updated_at = NOW()
			RETURNING (discovered_at = last_seen_at) AS is_new
		`,
			file.Path,
			file.Size,
			file.ModTime,
			sessionID,
			file.SourceRoot,
			file.MediaType,
			file.Extension,
			ScanStatusDiscovered,
			PathTypeRelative,
			FileOriginSource, // Source scans always produce SOURCE-origin files
			file.CaptureStem,
			file.CaptureRole,
		)
	}

	// Execute batch
	br := tx.SendBatch(ctx, batchQuery)

	// Process results — use QueryRow to read the RETURNING column.
	// discovered_at is set only on INSERT (never touched by ON CONFLICT UPDATE),
	// while last_seen_at is always set to NOW(). Within a single transaction NOW()
	// is constant, so discovered_at = last_seen_at iff the row was freshly inserted.
	for i := range batch {
		var isNew bool
		if err := br.QueryRow().Scan(&isNew); err != nil {
			s.log.Warn("Failed to insert file", "path", batch[i].Path, "error", err)
			atomic.AddInt64(&st.errors, 1)
		} else if isNew {
			atomic.AddInt64(&st.newFiles, 1)
		} else {
			// Existing row — count as skipped (unchanged).
			// A full "modified" detection would require reading the old row first;
			// for progress tracking purposes this is sufficient.
			atomic.AddInt64(&st.skipped, 1)
		}
	}

	// Must close BatchResults before Commit; otherwise the connection is still
	// considered busy and Commit returns "conn busy".
	if err := br.Close(); err != nil {
		s.log.Error("Failed to close batch results", "error", err)
		atomic.AddInt64(&st.errors, int64(len(batch)))
		return
	}

	if err := tx.Commit(ctx); err != nil {
		s.log.Error("Failed to commit batch", "error", err)
		atomic.AddInt64(&st.errors, int64(len(batch)))
		return
	}

	s.log.Debug("Batch inserted", "count", len(batch))
}

func (s *Scanner) cleanupDeletedFiles(ctx context.Context, sessionID uuid.UUID, rootPaths []string) error {
	s.log.Info("Cleaning up deleted files")

	result, err := s.db.Exec(ctx, `
		DELETE FROM file_registry
		WHERE source_root = ANY($1)
		  AND scan_session_id != $2
	`, rootPaths, sessionID)

	if err != nil {
		s.log.Error("Failed to cleanup deleted files", "error", err)
		return err
	}

	deleted := result.RowsAffected()
	if deleted > 0 {
		s.log.Info("Deleted files cleaned up", "count", deleted)
	}
	return nil
}

func (s *Scanner) updateProgress(ctx context.Context, sessionID uuid.UUID, st *scanState) {
	ticker := time.NewTicker(s.config.ProgressInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := s.db.Exec(ctx, `
				UPDATE scan_sessions
				SET files_discovered = $1,
					files_skipped = $2,
					files_new = $3,
					files_modified = $4,
					errors_encountered = $5
				WHERE id = $6
			`,
				atomic.LoadInt64(&st.discovered),
				atomic.LoadInt64(&st.skipped),
				atomic.LoadInt64(&st.newFiles),
				atomic.LoadInt64(&st.modified),
				atomic.LoadInt64(&st.errors),
				sessionID,
			)

			if err != nil {
				s.log.Warn("Failed to update progress", "error", err)
			}
		}
	}
}

func (s *Scanner) completeScan(ctx context.Context, session *ScanSession, finalStatus string, lastError *string, st *scanState) {
	s.log.Info("Completing scan", "session_id", session.ID, "status", finalStatus)

	_, err := s.db.Exec(ctx, `
		UPDATE scan_sessions
		SET completed_at = NOW(),
			status = $1,
			files_discovered = $2,
			files_skipped = $3,
			files_new = $4,
			files_modified = $5,
			errors_encountered = $6,
			last_error = $7
		WHERE id = $8
	`,
		finalStatus,
		atomic.LoadInt64(&st.discovered),
		atomic.LoadInt64(&st.skipped),
		atomic.LoadInt64(&st.newFiles),
		atomic.LoadInt64(&st.modified),
		atomic.LoadInt64(&st.errors),
		lastError,
		session.ID,
	)

	if err != nil {
		s.log.Error("Failed to complete scan", "error", err)
		return
	}

	s.log.Info("Scan finished", "session_id", session.ID, "status", finalStatus,
		"discovered", atomic.LoadInt64(&st.discovered),
		"new", atomic.LoadInt64(&st.newFiles),
		"skipped", atomic.LoadInt64(&st.skipped),
		"modified", atomic.LoadInt64(&st.modified),
		"errors", atomic.LoadInt64(&st.errors))

	if finalStatus == ScanStatusCompleted {
		s.enqueueHashJobs(ctx, session.ID)
	}
}

// hashJobArgs is a local mirror of hasher.HashTaskArgs.
// Defined here to avoid a circular import (scanner ← hasher).
// The Kind() and JSON field names must stay in sync with hasher.HashTaskArgs.
type hashJobArgs struct {
	SessionID string `json:"sessionId"`
	FileID    int64  `json:"fileId"`
	FilePath  string `json:"filePath"`
}

func (hashJobArgs) Kind() string { return "hash_task" }

// InsertOpts routes hash jobs to the dedicated hash queue, keeping them
// separate from scan jobs so they can be throttled independently.
func (hashJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue.HashQueue}
}

// enqueueHashJobs pages through all DISCOVERED files for the given session
// and enqueues a HashTask job for each one. Running after the scan walk
// is complete means zero I/O contention with the filesystem walk.
// Jobs are enqueued in pages of 100 so we never hold a large result set.
func (s *Scanner) enqueueHashJobs(ctx context.Context, sessionID uuid.UUID) {
	const pageSize = 100
	var lastID int64

	for {
		rows, err := s.db.Query(ctx, `
			SELECT id, file_path, source_root, path_type
			FROM file_registry
			WHERE scan_session_id = $1
			  AND scan_status    = $2
			  AND id             > $3
			ORDER BY id
			LIMIT $4
		`, sessionID, ScanStatusDiscovered, lastID, pageSize)
		if err != nil {
			s.log.Error("enqueueHashJobs: query failed", "session_id", sessionID, "error", err)
			return
		}

		type row struct {
			id         int64
			filePath   string
			sourceRoot string
			pathType   string
		}

		var page []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.filePath, &r.sourceRoot, &r.pathType); err != nil {
				s.log.Warn("enqueueHashJobs: scan row failed", "error", err)
				continue
			}
			page = append(page, r)
		}
		rows.Close()

		if len(page) == 0 {
			break
		}

		for _, r := range page {
			// Resolve to absolute path for the hasher.
			absPath := r.filePath
			if r.pathType == PathTypeRelative {
				absPath = s.pathUtil.MakeAbsolute(r.filePath, r.sourceRoot)
			}

			if err := s.jobQueue.Enqueue(ctx, hashJobArgs{
				SessionID: sessionID.String(),
				FileID:    r.id,
				FilePath:  absPath,
			}); err != nil {
				s.log.Warn("enqueueHashJobs: enqueue failed",
					"file_id", r.id,
					"error", err)
			}
		}

		lastID = page[len(page)-1].id
		if len(page) < pageSize {
			break
		}
	}

	s.log.Info("enqueueHashJobs: done", "session_id", sessionID)
}

// CleanupOrganizedFiles checks every ORGANIZED entry in the registry and removes
// those whose files no longer exist on disk. It does NOT re-index or re-classify
// any files — it is a pure deletion pass for stale entries.
// Returns the number of registry rows deleted.
func (s *Scanner) CleanupOrganizedFiles(ctx context.Context) (int64, error) {
	type entry struct {
		ID         int64
		FilePath   string
		SourceRoot string
		PathType   string
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, file_path, source_root, path_type
		FROM file_registry
		WHERE file_origin = $1
	`, FileOriginOrganized)
	if err != nil {
		return 0, fmt.Errorf("failed to query organized files: %w", err)
	}
	defer rows.Close()

	var missing []int64
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.FilePath, &e.SourceRoot, &e.PathType); err != nil {
			return 0, fmt.Errorf("failed to scan row: %w", err)
		}

		var absPath string
		if e.PathType == PathTypeAbsolute {
			absPath = e.FilePath
		} else {
			absPath = s.pathUtil.MakeAbsolute(e.FilePath, e.SourceRoot)
		}

		if _, statErr := os.Stat(absPath); os.IsNotExist(statErr) {
			missing = append(missing, e.ID)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("row iteration error: %w", err)
	}

	if len(missing) == 0 {
		s.log.Info("Organized library cleanup: no stale entries found")
		return 0, nil
	}

	result, err := s.db.Exec(ctx, `
		DELETE FROM file_registry WHERE id = ANY($1)
	`, missing)
	if err != nil {
		return 0, fmt.Errorf("failed to delete stale organized entries: %w", err)
	}

	deleted := result.RowsAffected()
	s.log.Info("Organized library cleanup complete", "deleted", deleted)
	return deleted, nil
}
