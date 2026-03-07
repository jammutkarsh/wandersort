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
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/status"
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
	statusMgr *status.StatusManager
}

// NewHasher creates a new hasher instance
func NewHasher(appCtx context.Context, db *db.DB, log logger.Logger, sm *status.StatusManager) *Hasher {
	if appCtx == nil {
		appCtx = context.Background()
	}

	return &Hasher{
		appCtx:    appCtx,
		db:        db,
		log:       log,
		scorer:    NewScorer(db, log),
		statusMgr: sm,
	}
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
func (h *Hasher) HashAll(ctx context.Context, sessionID uuid.UUID, files []FileRecord, workers int) {
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

	var sessionHashed uint64
	var mu sync.Mutex

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
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
						h.log.Warn("HashAll: failed to process file", "session_id", sessionID, "file_id", f.ID, "error", err)
						continue
					}

					mu.Lock()
					sessionHashed++
					n := sessionHashed
					mu.Unlock()

					if n%1000 == 0 {
						h.log.Info("Hashing milestone", "session_id", sessionID, "files_hashed", n)
					}
				}
			}
		}()
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
