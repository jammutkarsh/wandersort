package scanner

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/jammutkarsh/wandersort/pkg/core/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/status"
)

// Scanner handles file discovery and registry population.
// It is stateless with respect to individual scan runs; all mutable state
// lives in scanState, which is created fresh for every call to runScan.
// This makes concurrent scans safe without any locking on Scanner itself.
type Scanner struct {
	db         *db.DB
	classifier *classifier.FileClassifier
	log        logger.Logger
	statusMgr  *status.StatusManager
	config     ScanConfig
	pathUtil   *PathUtil
	outputPath string

	// activeSessions tracks the state of ongoing multi-directory scans
	activeSessions sync.Map // map[uuid.UUID]*scanSessionTracker
}

// scanSessionTracker tracks the progress of a multi-directory scan session.
type scanSessionTracker struct {
	sessionID uuid.UUID

	discovered atomic.Int64
	skipped    atomic.Int64
	newFiles   atomic.Int64
	modified   atomic.Int64
	errors     atomic.Int64

	unsupported      int64
	unsupportedMu    sync.Mutex
	unsupportedPaths []string

	pendingJobs atomic.Int32
	ctx         context.Context
	cancel      context.CancelFunc
}

type scanState struct {
	tracker *scanSessionTracker
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
func NewScanner(db *db.DB, log logger.Logger, outputPath string, sm *status.StatusManager) *Scanner {
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
		statusMgr:  sm,
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

// PrepareSession validates rootPaths, creates the scan_sessions DB row, and
// returns the sessionID and expanded (absolute) root paths.
// It initializes the shared tracker for all jobs in this session.
func (s *Scanner) PrepareSession(ctx context.Context, rootPaths []string) (uuid.UUID, []string, error) {
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

	// Convert root paths to JSON for storage
	rootPathsJSON, err := json.Marshal(rootPaths)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to marshal root paths: %w", err)
	}

	// Insert session into DB
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO scan_sessions (id, started_at, status, root_paths)
		VALUES (?, ?, ?, ?)
	`, sessionID, startedAt.Format(time.RFC3339), ScanStatusScan, string(rootPathsJSON))
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to create scan session: %w", err)
	}

	s.log.Info("Scan session created", "session_id", sessionID, "root_paths", rootPaths)

	// Initialize the shared state tracker for this session
	progressCtx, cancelProgress := context.WithCancel(ctx)
	tracker := &scanSessionTracker{
		sessionID: sessionID,
		ctx:       progressCtx,
		cancel:    cancelProgress,
	}
	tracker.pendingJobs.Store(int32(len(rootPaths)))
	s.activeSessions.Store(sessionID, tracker)

	// Start the periodic progress updater for this session
	go s.updateProgress(progressCtx, sessionID, tracker)

	return sessionID, expandedRoots, nil
}

// ScanSinglePath executes a scan for a single directory path and returns a channel
// of discovered files. It's meant to be called by worker goroutines asynchronously.
func (s *Scanner) ScanSinglePath(ctx context.Context, sessionID uuid.UUID, expandedRoot, originalRoot string) (<-chan FileDiscovery, error) {
	v, ok := s.activeSessions.Load(sessionID)
	if !ok {
		return nil, fmt.Errorf("scan session %s not found or already completed", sessionID)
	}
	tracker := v.(*scanSessionTracker)
	st := &scanState{tracker: tracker}

	// Channel for discovered files
	filesChan := make(chan FileDiscovery, s.config.WorkerBufferSize)
	outChan := make(chan FileDiscovery, s.config.WorkerBufferSize)

	go func() {
		defer close(filesChan)

		if err := s.walkRoot(ctx, expandedRoot, originalRoot, filesChan, st); err != nil {
			s.log.Error("Walk root failed during ScanSinglePath", "path", expandedRoot, "error", err)
		}
	}()

	// Process discovered files in the background and pipe them to outChan
	go func() {
		s.processDiscoveries(ctx, sessionID, filesChan, st, outChan)
	}()

	return outChan, nil
}

// MarkJobComplete decrements the pending job count for a session.
// Once all scan jobs are complete, it writes the unsupported-file report and
// stops periodic scan-progress updates. Session finalization happens later,
// after hash/score phases, via FinalizeSession.
func (s *Scanner) MarkJobComplete(ctx context.Context, sessionID uuid.UUID, expandedRoot string) {
	v, ok := s.activeSessions.Load(sessionID)
	if !ok {
		s.log.Warn("MarkJobComplete called for unknown session", "session_id", sessionID, "path", expandedRoot)
		return
	}
	tracker := v.(*scanSessionTracker)

	pending := tracker.pendingJobs.Add(-1)
	s.log.Debug("Job completed", "session_id", sessionID, "path", expandedRoot, "pending_jobs_remaining", pending)

	if pending == 0 {
		// All paths for this session have been processed.
		s.log.Info("All scan jobs complete for session", "session_id", sessionID)

		// Stop progress updater; scan counters are now stable.
		tracker.cancel()

		s.writeUnsupportedFiles(sessionID, tracker)
	}
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
			st.tracker.errors.Add(1)
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
			atomic.AddInt64(&st.tracker.unsupported, 1)
			st.tracker.unsupportedMu.Lock()
			st.tracker.unsupportedPaths = append(st.tracker.unsupportedPaths, path)
			st.tracker.unsupportedMu.Unlock()
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			s.log.Warn("Failed to get file info", "path", path, "error", err)
			st.tracker.errors.Add(1)
			return nil
		}

		// Make path relative to the expanded root
		relPath, err := s.pathUtil.MakeRelative(path, expandedRoot)
		if err != nil {
			s.log.Warn("Failed to make path relative", "path", path, "error", err)
			st.tracker.errors.Add(1)
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

		st.tracker.discovered.Add(1)

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
func (s *Scanner) writeUnsupportedFiles(sessionID uuid.UUID, tracker *scanSessionTracker) {
	tracker.unsupportedMu.Lock()
	paths := make([]string, len(tracker.unsupportedPaths))
	copy(paths, tracker.unsupportedPaths)
	tracker.unsupportedMu.Unlock()

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

func (s *Scanner) processDiscoveries(ctx context.Context, sessionID uuid.UUID, discoveries <-chan FileDiscovery, st *scanState, emitChan chan<- FileDiscovery) {
	var pendingWrites sync.WaitGroup
	var emitterWG sync.WaitGroup
	emitJobs := make(chan FileDiscovery, s.config.WorkerBufferSize)

	const emitWorkers = 4
	for range emitWorkers {
		emitterWG.Go(func() {
			for file := range emitJobs {
				select {
				case emitChan <- file:
				case <-ctx.Done():
					return
				}
			}
		})
	}

	defer func() {
		pendingWrites.Wait()
		close(emitJobs)
		emitterWG.Wait()
		close(emitChan)
	}()

	const upsertQuery = `
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
		RETURNING id, (discovered_at = last_seen_at) AS is_new
	`

	for discovery := range discoveries {
		file := discovery // shadow for closure capturing
		pendingWrites.Add(1)
		if !s.db.Writer.Write(func(dbCtx context.Context, tx *sql.Tx) error {
			defer pendingWrites.Done()

			// Ensure we respect overall context
			select {
			case <-dbCtx.Done():
				return dbCtx.Err()
			default:
			}

			var id int64
			var isNew int
			err := tx.QueryRowContext(dbCtx, upsertQuery,
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
				file.CaptureStem,
				file.CaptureRole,
			).Scan(&id, &isNew)

			if err != nil {
				s.log.Warn("Failed to upsert file", "path", file.Path, "error", err)
				st.tracker.errors.Add(1)
				return nil // Don't block batch execution for one bad file
			}

			if isNew == 1 {
				st.tracker.newFiles.Add(1)
			} else {
				st.tracker.skipped.Add(1) // Technically it might be modified
			}

			file.ID = id

			// Hand off discovered file IDs to a fixed-size emitter pool to avoid
			// spawning one goroutine per discovery.
			select {
			case emitJobs <- file:
			case <-ctx.Done():
			}

			return nil
		}) {
			pendingWrites.Done()
			st.tracker.errors.Add(1)
			s.log.Warn("Bulk writer closed; dropping discovery write", "path", file.Path, "session_id", sessionID)
		}
	}
}

func (s *Scanner) cleanupDeletedFiles(ctx context.Context, sessionID uuid.UUID, rootPaths []string) error {
	s.log.Info("Cleaning up deleted files")

	placeholders, args := db.InClause(rootPaths)
	args = append(args, sessionID.String())

	query := fmt.Sprintf(`
		DELETE FROM file_registry
		WHERE source_root IN (%s)
		  AND scan_session_id != ?
	`, placeholders)

	result, err := s.execWithBusyRetry(ctx, query, args...)
	if err != nil {
		s.log.Error("Failed to cleanup deleted files", "error", err)
		return err
	}

	deleted, _ := result.RowsAffected()
	if deleted > 0 {
		s.log.Info("Deleted files cleaned up", "count", deleted)
	}
	return nil
}

// CleanupDeletedFiles removes registry rows for files that were not observed in
// the latest scan session for the provided roots.
func (s *Scanner) CleanupDeletedFiles(ctx context.Context, sessionID uuid.UUID, rootPaths []string) error {
	return s.cleanupDeletedFiles(ctx, sessionID, rootPaths)
}

// SetSessionStatus updates the current phase/status for a scan session.
func (s *Scanner) SetSessionStatus(ctx context.Context, sessionID uuid.UUID, statusValue string) error {
	_, err := s.execWithBusyRetry(ctx, `
		UPDATE scan_sessions
		SET status = ?
		WHERE id = ?
	`, statusValue, sessionID.String())
	if err != nil {
		return fmt.Errorf("set session status: %w", err)
	}

	if s.statusMgr != nil {
		current := s.statusMgr.GetCurrent()
		if current.SessionID != sessionID {
			current = status.PipelineStatus{SessionID: sessionID}
		}
		current.Status = statusValue
		s.statusMgr.Broadcast(current)
	}
	return nil
}

func (s *Scanner) updateProgress(ctx context.Context, sessionID uuid.UUID, tracker *scanSessionTracker) {
	ticker := time.NewTicker(s.config.ProgressInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := s.execWithBusyRetry(ctx, `
					UPDATE scan_sessions
					SET files_discovered = ?,
						files_skipped = ?,
					files_new = ?,
					files_modified = ?,
					errors_encountered = ?
				WHERE id = ?
			`,
				tracker.discovered.Load(),
				tracker.skipped.Load(),
				tracker.newFiles.Load(),
				tracker.modified.Load(),
				tracker.errors.Load(),
				sessionID.String(),
			)

			if s.statusMgr != nil {
				s.statusMgr.Broadcast(status.PipelineStatus{
					SessionID:       sessionID,
					Status:          ScanStatusScan,
					FilesDiscovered: tracker.discovered.Load(),
					FilesSkipped:    tracker.skipped.Load(),
					FilesNew:        tracker.newFiles.Load(),
					Errors:          tracker.errors.Load(),
				})
			}

			if err != nil {
				s.log.Warn("Failed to update progress", "error", err)
			}
		}
	}
}

func (s *Scanner) completeScan(ctx context.Context, sessionID uuid.UUID, finalStatus string, lastError *string, tracker *scanSessionTracker) error {
	s.log.Info("Completing scan", "session_id", sessionID, "status", finalStatus)

	completedAt := time.Now().UTC().Format(time.RFC3339)
	_, err := s.execWithBusyRetry(ctx, `
		UPDATE scan_sessions
		SET completed_at = ?,
			status = ?,
			files_discovered = ?,
			files_skipped = ?,
			files_new = ?,
			files_modified = ?,
			errors_encountered = ?,
			last_error = ?
		WHERE id = ?
	`,
		completedAt,
		finalStatus,
		tracker.discovered.Load(),
		tracker.skipped.Load(),
		tracker.newFiles.Load(),
		tracker.modified.Load(),
		tracker.errors.Load(),
		lastError,
		sessionID.String(),
	)
	if err != nil {
		return fmt.Errorf("failed to complete scan: %w", err)
	}

	if s.statusMgr != nil {
		var filesHashed int64
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(files_hashed, 0) FROM scan_sessions WHERE id = ?`, sessionID.String()).Scan(&filesHashed); err != nil {
			s.log.Warn("Failed to read files_hashed while broadcasting final status", "session_id", sessionID, "error", err)
		}
		s.statusMgr.Broadcast(status.PipelineStatus{
			SessionID:       sessionID,
			Status:          finalStatus,
			FilesDiscovered: tracker.discovered.Load(),
			FilesSkipped:    tracker.skipped.Load(),
			FilesNew:        tracker.newFiles.Load(),
			FilesHashed:     filesHashed,
			Errors:          tracker.errors.Load(),
		})
	}

	s.log.Info("Scan finished", "session_id", sessionID, "status", finalStatus,
		"discovered", tracker.discovered.Load(),
		"new", tracker.newFiles.Load(),
		"skipped", tracker.skipped.Load(),
		"modified", tracker.modified.Load(),
		"errors", tracker.errors.Load())
	return nil
}

// FinalizeSession marks a session terminally complete after all pipeline phases
// (scan, hash, score) are done.
func (s *Scanner) FinalizeSession(ctx context.Context, sessionID uuid.UUID, finalStatus string, lastError *string) error {
	v, ok := s.activeSessions.Load(sessionID)
	if !ok {
		s.log.Warn("FinalizeSession called for unknown session", "session_id", sessionID)
		completedAt := time.Now().UTC().Format(time.RFC3339)
		_, err := s.execWithBusyRetry(ctx, `
			UPDATE scan_sessions
			SET completed_at = ?, status = ?, last_error = ?
			WHERE id = ?
		`, completedAt, finalStatus, lastError, sessionID.String())
		if err != nil {
			return fmt.Errorf("failed to finalize session without tracker: %w", err)
		}
		return nil
	}

	tracker := v.(*scanSessionTracker)
	tracker.cancel()
	s.activeSessions.Delete(sessionID)

	return s.completeScan(ctx, sessionID, finalStatus, lastError, tracker)
}

func (s *Scanner) execWithBusyRetry(ctx context.Context, query string, args ...any) (sql.Result, error) {
	const maxAttempts = 12
	backoff := 50 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := s.db.ExecContext(ctx, query, args...)
		if err == nil {
			return result, nil
		}
		lastErr = err

		if !isSQLITEBusy(err) || attempt == maxAttempts {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}

		// Exponential backoff capped to keep retries bounded.
		if backoff < 500*time.Millisecond {
			backoff *= 2
		}
	}

	return nil, lastErr
}

func isSQLITEBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

// CleanupOrganizedFiles checks every ORGANIZED entry in the registry and removes
// those whose files no longer exist on disk. It does NOT re-index or re-classify
// any files — it is a pure deletion pass for stale entries.
// Rows are processed in cursor-based pages so memory stays bounded regardless
// of table size. Returns the number of registry rows deleted.
func (s *Scanner) CleanupOrganizedFiles(ctx context.Context) (int64, error) {
	const pageSize = 500
	var lastID int64
	var totalDeleted int64

	for {
		type entry struct {
			ID         int64
			FilePath   string
			SourceRoot string
			PathType   string
		}

		rows, err := s.db.QueryContext(ctx, `
			SELECT id, file_path, source_root, path_type
			FROM file_registry
			WHERE file_origin = ? AND id > ?
			ORDER BY id
			LIMIT ?
		`, FileOriginOrganized, lastID, pageSize)
		if err != nil {
			return totalDeleted, fmt.Errorf("failed to query organized files: %w", err)
		}

		var page []entry
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.ID, &e.FilePath, &e.SourceRoot, &e.PathType); err != nil {
				rows.Close()
				return totalDeleted, fmt.Errorf("failed to scan row: %w", err)
			}
			page = append(page, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return totalDeleted, fmt.Errorf("row iteration error: %w", err)
		}

		if len(page) == 0 {
			break
		}

		var missing []int64
		for _, e := range page {
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

		if len(missing) > 0 {
			placeholders, args := db.InClause(missing)
			query := fmt.Sprintf(`DELETE FROM file_registry WHERE id IN (%s)`, placeholders)
			result, err := s.db.ExecContext(ctx, query, args...)
			if err != nil {
				return totalDeleted, fmt.Errorf("failed to delete stale organized entries: %w", err)
			}
			deleted, _ := result.RowsAffected()
			totalDeleted += deleted
		}

		lastID = page[len(page)-1].ID
		if len(page) < pageSize {
			break
		}
	}

	if totalDeleted == 0 {
		s.log.Info("Organized library cleanup: no stale entries found")
	} else {
		s.log.Info("Organized library cleanup complete", "deleted", totalDeleted)
	}
	return totalDeleted, nil
}
