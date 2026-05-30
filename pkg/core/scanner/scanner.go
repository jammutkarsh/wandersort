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
	"github.com/jammutkarsh/wandersort/pkg/core/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/statusmanager"
)

// Scanner handles file discovery and registry population.
// It is stateless with respect to individual scan runs; all mutable state
// lives in scanState, which is created fresh for every call to runScan.
// This makes concurrent scans safe without any locking on Scanner itself.
type Scanner struct {
	db         *db.DB
	classifier *classifier.FileClassifier
	log        logger.Logger
	config     ScanConfig
	path       *path.Resolver
}

type scanState struct {
	tracker *statusmanager.SessionTracker
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
func NewScanner(db *db.DB, log logger.Logger) *Scanner {
	return &Scanner{
		db:         db,
		classifier: classifier.NewFileClassifier(),
		log:        log,
		path:       path.New(),
		config: ScanConfig{
			MaxWalkers:       4,
			WorkerBufferSize: 1000,
			BatchInsertSize:  500,
			ProgressInterval: 5 * time.Second,
		},
	}
}

// ScanSinglePath executes a scan for a single directory path and returns a channel
// of discovered files. It's meant to be called by worker goroutines asynchronously.
func (s *Scanner) ScanSinglePath(ctx context.Context, tracker *statusmanager.SessionTracker, path string) (<-chan FileDiscovery, error) {
	st := &scanState{tracker: tracker}

	// Channel for discovered files
	filesChan := make(chan FileDiscovery, s.config.WorkerBufferSize)
	outChan := make(chan FileDiscovery, s.config.WorkerBufferSize)

	go func() {
		defer close(filesChan)

		if err := s.walkRoot(ctx, path, filesChan, st); err != nil {
			s.log.Error("Walk root failed during ScanSinglePath", "path", path, "error", err)
		}
	}()

	// Process discovered files in the background and pipe them to outChan
	go func() {
		s.processDiscoveries(ctx, tracker.SessionID, filesChan, st, outChan)
	}()

	return outChan, nil
}

// MarkJobComplete decrements the pending job count for a session.
// Once all scan jobs are complete, it stops periodic scan-progress updates.
func (s *Scanner) MarkJobComplete(ctx context.Context, tracker *statusmanager.SessionTracker, path string) {
	pending := tracker.PendingJobs.Add(-1)
	s.log.Debug("Job completed", "session_id", tracker.SessionID, "path", path, "pending_jobs_remaining", pending)

	if pending == 0 {
		// All paths for this session have been processed.
		s.log.Info("All scan jobs complete for session", "session_id", tracker.SessionID)

		// Stop progress updater; scan counters are now stable.
		tracker.Cancel()
	}
}

// walkRoot walks absPath and emits FileDiscovery records with relative paths.
// absPath is the absolute filesystem path.
func (s *Scanner) walkRoot(ctx context.Context, path string, output chan<- FileDiscovery, st *scanState) error {
	absRoot, err := s.path.RealPath(path)
	if err != nil {
		s.log.Error("Failed to resolve root path", "input_path", path, "error", err)
		return err
	}

	rootDisplayPath := s.path.ContractPath(absRoot)
	s.log.Info("Starting walk", "input_path", rootDisplayPath)

	err = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Handle errors (permission denied, etc.)
		if err != nil {
			s.log.Warn("Walk error", "input_path", rootDisplayPath, "walking_path", s.path.ContractPath(p), "error", err)
			st.tracker.Errors.Add(1)
			return nil // Continue walking
		}

		// Skip ignored directories
		if d.IsDir() {
			if s.classifier.ShouldIgnoreDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip ignored files
		if s.classifier.ShouldIgnore(d.Name()) {
			return nil
		}

		// Resolve path for classification and any operation that requires an
		// absolute location.
		realPath, err := s.path.RealPath(p)
		if err != nil {
			s.log.Warn("Failed to resolve file path", "input_path", rootDisplayPath, "walking_path", s.path.ContractPath(p), "error", err)
			st.tracker.Errors.Add(1)
			return nil
		}

		// Classify file
		mediaType, shouldProcess := s.classifier.Classify(realPath)
		if !shouldProcess {
			st.tracker.Unsupported.Add(1)
			st.tracker.UnsupportedMu.Lock()
			st.tracker.UnsupportedPaths = append(st.tracker.UnsupportedPaths, s.path.ContractPath(realPath))
			st.tracker.UnsupportedMu.Unlock()
			return nil
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			s.log.Warn("Failed to get file info", "input_path", rootDisplayPath, "walking_path", s.path.ContractPath(p), "error", err)
			st.tracker.Errors.Add(1)
			return nil
		}

		relPath, err := filepath.Rel(absRoot, p)
		if err != nil {
			s.log.Warn("Failed to make path relative", "input_path", rootDisplayPath, "walking_path", s.path.ContractPath(p), "error", err)
			st.tracker.Errors.Add(1)
			return nil
		}

		// Persist file path relative to source root for portability.
		capture := DeriveCapture(d.Name(), strings.ToLower(filepath.Ext(p)), mediaType)
		discovery := FileDiscovery{
			Path:        relPath,
			Size:        info.Size(),
			ModTime:     info.ModTime(),
			Extension:   strings.ToLower(filepath.Ext(p)),
			SourceRoot:  rootDisplayPath,
			MediaType:   mediaType,
			CaptureStem: capture.Stem, // Derived from filename
			CaptureRole: capture.Role, // Derived from filename and media type
		}

		st.tracker.Discovered.Add(1)

		// Send to processing channel
		select {
		case output <- discovery:
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	})

	if err != nil {
		s.log.Error("Walk failed", "root", rootDisplayPath, "error", err)
		return err
	}
	return nil
}

func (s *Scanner) RunPhase(ctx context.Context, sessionID uuid.UUID, tracker *statusmanager.SessionTracker, paths []string) (int, error) {
	s.log.Info("Scanner Phase: Processing all paths", "session_id", sessionID, "path_count", len(paths))
	type scanResult struct {
		count int
		err   error
	}

	jobs := make(chan string, len(paths))
	results := make(chan scanResult, len(paths))

	// Determine worker count based on path count, but capped at MaxWalkers
	workerCount := len(paths)
	if workerCount > s.config.MaxWalkers {
		workerCount = s.config.MaxWalkers
	}
	if workerCount <= 0 {
		workerCount = 1
	}

	var workers sync.WaitGroup
	for range workerCount {
		workers.Go(func() {
			for path := range jobs {
				// Scan the individual path
				discoveredChan, err := s.ScanSinglePath(ctx, tracker, path)
				if err != nil {
					s.log.Error("Failed to scan path", "session_id", sessionID, "path", path, "error", err)
					s.MarkJobComplete(ctx, tracker, path)
					results <- scanResult{count: 0, err: fmt.Errorf("scan failed for %s: %w", path, err)}
					continue
				}

				count := 0
				for range discoveredChan {
					count++
				}

				s.MarkJobComplete(ctx, tracker, path)
				s.log.Info("Scanned path", "session_id", sessionID, "path", path, "files_discovered", count)
				results <- scanResult{count: count, err: nil}
			}
		})
	}

	for _, path := range paths {
		jobs <- path
	}
	close(jobs)
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

			var err error
			var id int64
			var isNew int
			err = tx.QueryRowContext(dbCtx, upsertQuery,
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
				st.tracker.Errors.Add(1)
				return nil // Don't block batch execution for one bad file
			}

			if isNew == 1 {
				st.tracker.NewFiles.Add(1)
			} else {
				st.tracker.Skipped.Add(1) // Technically it might be modified
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
			st.tracker.Errors.Add(1)
			s.log.Warn("Bulk writer closed; dropping discovery write", "path", file.Path, "session_id", sessionID)
		}
	}
}
