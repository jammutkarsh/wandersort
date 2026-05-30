package hasher

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/statusmanager"
	"lukechampine.com/blake3"
)

// FileRecord is the minimal info the pipeline passes from the scan phase
// to drive the hash phase.
type FileRecord struct {
	ID      int64
	AbsPath string
}

// Hasher handles file hashing and content group management
type Hasher struct {
	appCtx    context.Context
	db        *db.DB
	log       logger.Logger
	scorer    *Scorer
	statusMgr *statusmanager.StatusManager
	path      *path.Resolver
}

// NewHasher creates a new hasher instance
func NewHasher(appCtx context.Context, db *db.DB, log logger.Logger, sm *statusmanager.StatusManager) *Hasher {
	if appCtx == nil {
		appCtx = context.Background()
	}

	return &Hasher{
		appCtx:    appCtx,
		db:        db,
		log:       log,
		scorer:    NewScorer(db, log),
		statusMgr: sm,
		path:      path.New(),
	}
}

func (h *Hasher) ScorePaths(ctx context.Context, sessionID uuid.UUID, paths []string, workers int) (int, error) {
	h.scorer.statusMgr = h.statusMgr
	return h.scorer.ScorePaths(ctx, sessionID, paths, workers)
}

// HashFile computes the BLAKE3 hash of a file
// Uses streaming to handle files of any size with constant memory (~32KB buffer)
func (h *Hasher) HashFile(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := blake3.New(32, nil) // 32-byte output → 64 hex chars, matching CHAR(64) column

	// Stream copy - reads in 32KB chunks by default
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	sum := make([]byte, 0, 32)
	hash := hex.EncodeToString(hasher.Sum(sum))
	return hash, nil
}

// ProcessFile hashes a file and enqueues all DB mutations via the bulk writer.
func (h *Hasher) ProcessFile(ctx context.Context, sessionID uuid.UUID, fileID int64, filePath string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Compute hash (CPU-bound; done synchronously before any DB work).
	hash, err := h.HashFile(filePath)
	if err != nil {
		h.log.Error("Failed to hash file", "file_id", fileID, "path", filePath, "error", err)
		h.markFileError(fileID)
		return err
	}

	sessionStr := sessionID.String()
	if !h.db.Writer.Write(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE file_registry
			SET file_hash = ?, scan_status = 'HASHED'
			WHERE id = ?
		`, hash, fileID); err != nil {
			return fmt.Errorf("failed to update file registry: %w", err)
		}

		groupID, err := h.getOrCreateGroup(ctx, tx, hash)
		if err != nil {
			return fmt.Errorf("failed to create/get content group: %w", err)
		}

		if err := h.addMemberToGroup(ctx, tx, groupID, fileID); err != nil {
			return fmt.Errorf("failed to add member to group: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE scan_sessions
			SET files_hashed = COALESCE(files_hashed, 0) + 1
			WHERE id = ?
		`, sessionStr); err != nil {
			return fmt.Errorf("failed to update files_hashed counter: %w", err)
		}

		return nil
	}) {
		return fmt.Errorf("bulk writer closed")
	}

	return nil
}

// getOrCreateGroup atomically creates a new content group or returns the existing one.
// Uses INSERT ... ON CONFLICT to avoid the TOCTOU race that would occur with a
// separate SELECT-then-INSERT approach when multiple hashers process the same hash.
func (h *Hasher) getOrCreateGroup(ctx context.Context, tx *sql.Tx, hash string) (int64, error) {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO content_groups (content_hash, total_copies) VALUES (?, 1)
		ON CONFLICT (content_hash) DO UPDATE SET total_copies = content_groups.total_copies + 1`,
		hash,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to upsert content group: %w", err)
	}

	var groupID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM content_groups WHERE content_hash = ?`, hash).
		Scan(&groupID)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch content group: %w", err)
	}
	return groupID, nil
}

// addMemberToGroup adds a file to a content group
func (h *Hasher) addMemberToGroup(ctx context.Context, tx *sql.Tx, groupID, fileID int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO content_group_members (group_id, file_id, is_master, metadata_score)
		VALUES (?, ?, 0, 0)
		ON CONFLICT (group_id, file_id) DO NOTHING`,
		groupID, fileID,
	)
	return err
}

// HashAll hashes every file in the slice using a fixed-size worker pool.
func (h *Hasher) HashAll(ctx context.Context, sessionID uuid.UUID, files []FileRecord, workers int, tracker *statusmanager.SessionTracker) int {
	// Hashing must be tied to application lifecycle, not a request-scoped ctx.
	workCtx := h.appCtx
	if workCtx == nil {
		if ctx != nil {
			workCtx = ctx
		} else {
			workCtx = context.Background()
		}
	}

	if workers <= 0 {
		workers = 5
	}

	var wg sync.WaitGroup
	jobs := make(chan FileRecord)

	var batchHashed uint64
	var mu sync.Mutex

	for range workers {
		wg.Go(func() {
			for {
				select {
				case <-workCtx.Done():
					return
				case f, ok := <-jobs:
					if !ok {
						return
					}

					if err := h.ProcessFile(workCtx, sessionID, f.ID, f.AbsPath); err != nil {
						if isContextDone(err) {
							return
						}
						if tracker != nil {
							tracker.Errors.Add(1)
						}
						h.log.Warn("HashAll: failed to process file", "session_id", sessionID, "file_id", f.ID, "error", err)
						continue
					}

					mu.Lock()
					batchHashed++
					n := batchHashed
					mu.Unlock()

					if tracker != nil {
						tracker.Hashed.Add(1)
					}

					if n%1000 == 0 {
						h.log.Info("Hashing milestone", "session_id", sessionID, "files_hashed", n)
					}
				}
			}
		})
	}

enqueue:
	for _, f := range files {
		select {
		case <-workCtx.Done():
			break enqueue
		case jobs <- f:
		}
	}
	close(jobs)
	wg.Wait()

	// Flush all enqueued writes before returning so the next pipeline phase
	// sees fully committed data.
	h.db.Writer.Flush()

	return int(batchHashed)
}

// HashPaths fans out hashing work by source root and waits for every path job
// to finish before the pipeline advances to the next phase.
func (h *Hasher) HashPaths(ctx context.Context, sessionID uuid.UUID, paths []string, pathWorkers, fileWorkers int, tracker *statusmanager.SessionTracker) (int, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	if pathWorkers <= 0 {
		pathWorkers = 1
	}

	if tracker != nil {
		tracker.PendingJobs.Store(int32(len(paths)))
	}

	type hashResult struct {
		count int
		err   error
	}

	jobs := make(chan string, len(paths))
	results := make(chan hashResult, len(paths))

	var workers sync.WaitGroup
	for range pathWorkers {
		workers.Go(func() {
			for path := range jobs {
				count, err := h.HashPath(ctx, sessionID, path, fileWorkers, tracker)
				h.markJobComplete(sessionID, path, tracker)
				results <- hashResult{count: count, err: err}
			}
		})
	}

	for _, path := range paths {
		jobs <- path
	}
	close(jobs)
	workers.Wait()
	close(results)

	total := 0
	var firstErr error
	for result := range results {
		total += result.count
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
	}

	return total, firstErr
}

// HashPath fetches all hashable files for a source root in pages and executes
// hashing in bounded worker pools.
func (h *Hasher) HashPath(ctx context.Context, sessionID uuid.UUID, path string, workers int, tracker *statusmanager.SessionTracker) (int, error) {
	const pageSize = 1000
	var lastID int64
	var total int

	h.log.Info("Hashing path", "session_id", sessionID, "path", path)

	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}

		rows, err := h.db.QueryContext(ctx, `
			SELECT id, file_path, source_root, path_type
			FROM file_registry
			WHERE scan_session_id = ? AND id > ?
			  AND source_root = ?
			  AND scan_status NOT IN ('HASHED', 'ANALYZED', 'ANALYZING')
			ORDER BY id
			LIMIT ?
		`, sessionID.String(), lastID, path, pageSize)
		if err != nil {
			return total, fmt.Errorf("query hash batch: %w", err)
		}

		batch := make([]FileRecord, 0, pageSize)
		for rows.Next() {
			var (
				id         int64
				filePath   string
				sourceRoot string
				pathType   string
			)
			if err := rows.Scan(&id, &filePath, &sourceRoot, &pathType); err != nil {
				rows.Close()
				return total, fmt.Errorf("scan hash batch row: %w", err)
			}

			absPath := filePath
			if pathType != scanner.PathTypeAbsolute {
				absPath = h.path.MakeAbsolute(filePath, sourceRoot)
			}

			batch = append(batch, FileRecord{ID: id, AbsPath: absPath})
			lastID = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, fmt.Errorf("iterate hash batch rows: %w", err)
		}

		if len(batch) == 0 {
			break
		}

		total += h.HashAll(ctx, sessionID, batch, workers, tracker)

		if len(batch) < pageSize {
			break
		}
	}

	h.log.Info("Hashed path", "session_id", sessionID, "path", path, "files_hashed", total)
	return total, nil
}

// HashSessionFiles keeps the old session-oriented entry point as a convenience
// for callers that do not need per-path fan-out.
func (h *Hasher) HashSessionFiles(ctx context.Context, sessionID uuid.UUID, workers int) (int, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT DISTINCT source_root
		FROM file_registry
		WHERE scan_session_id = ?
		ORDER BY source_root
	`, sessionID.String())
	if err != nil {
		return 0, fmt.Errorf("query session source roots: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return 0, fmt.Errorf("scan session source root: %w", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate session source roots: %w", err)
	}

	pathWorkers := workers
	if len(paths) > pathWorkers {
		pathWorkers = len(paths)
	}

	return h.HashPaths(ctx, sessionID, paths, pathWorkers, workers, nil)
}

func (h *Hasher) markJobComplete(sessionID uuid.UUID, path string, tracker *statusmanager.SessionTracker) {
	if tracker == nil {
		return
	}
	pending := tracker.PendingJobs.Add(-1)
	h.log.Debug("Hash job completed", "session_id", sessionID, "path", path, "pending_jobs_remaining", pending)
	if pending == 0 {
		h.log.Info("All hash jobs complete for session", "session_id", sessionID)
	}
}

func (h *Hasher) markFileError(fileID int64) {
	h.db.Writer.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE file_registry
			SET scan_status = 'ERROR'
			WHERE id = ?
		`, fileID)
		return err
	})
}

func isContextDone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func normalizeContextErr(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return err
}

// ScoreAll runs the scoring phase across all content groups.
// Today this is a no-op stub; it will be implemented once metadata extraction
// is in place.
func (h *Hasher) ScoreAll(ctx context.Context, workers int) {
	_ = ctx
	_ = workers
	// TODO: query all content groups and fan out scorer.CalculateScore
	// once metadata extraction is implemented.
}
