package hasher

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

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
	db          *db.DB
	log         logger.Logger
	scorer      *Scorer
	statusMgr   *status.StatusManager
	totalHashed atomic.Int64 // lifetime counter across all sessions
}

// NewHasher creates a new hasher instance
func NewHasher(db *db.DB, log logger.Logger, sm *status.StatusManager) *Hasher {
	return &Hasher{
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

// ProcessFile hashes a file and updates the database
func (h *Hasher) ProcessFile(ctx context.Context, fileID int64, filePath string) error {
	// Compute hash
	hash, err := h.HashFile(filePath)
	if err != nil {
		h.log.Error("Failed to hash file", "file_id", fileID, "path", filePath, "error", err)

		h.db.Writer.Write(func(dbCtx context.Context, tx *sql.Tx) error {
			// Mark file as error in registry
			_, updateErr := tx.ExecContext(dbCtx, `
				UPDATE file_registry 
				SET scan_status = 'ERROR' 
				WHERE id = ?
			`, fileID)
			if updateErr != nil {
				h.log.Error("Failed to mark file as error", "file_id", fileID, "error", updateErr)
			}
			return nil
		})
		return err
	}

	h.db.Writer.Write(func(dbCtx context.Context, tx *sql.Tx) error {
		// Update file registry with hash
		_, err = tx.ExecContext(dbCtx, `
			UPDATE file_registry 
			SET file_hash = ?, scan_status = 'HASHED' 
			WHERE id = ?
		`, hash, fileID)
		if err != nil {
			return fmt.Errorf("failed to update file registry: %w", err)
		}

		// Create or get content group (uses INSERT ON CONFLICT for concurrency safety)
		groupID, _, err := h.getOrCreateGroup(dbCtx, tx, hash)
		if err != nil {
			return fmt.Errorf("failed to create/get content group: %w", err)
		}

		// Add file to group
		if err := h.addMemberToGroup(dbCtx, tx, groupID, fileID); err != nil {
			return fmt.Errorf("failed to add member to group: %w", err)
		}

		n := h.totalHashed.Add(1)

		// Milestone log every 1000 files so progress is visible without flooding logs
		if n%1000 == 0 {
			h.log.Info("Hashing milestone", "files_hashed", n)
			if h.statusMgr != nil {
				current := h.statusMgr.GetCurrent()
				current.FilesHashed = n
				h.statusMgr.Broadcast(current)
			}
		}
		return nil
	})

	return nil
}

// getOrCreateGroup atomically creates a new content group or returns the existing one.
// Uses INSERT ... ON CONFLICT to avoid the TOCTOU race that would occur with a
// separate SELECT-then-INSERT approach when multiple hashers process the same hash.
func (h *Hasher) getOrCreateGroup(ctx context.Context, tx *sql.Tx, hash string) (int64, bool, error) {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO content_groups (content_hash, total_copies) VALUES (?, 1)
		ON CONFLICT (content_hash) DO UPDATE SET total_copies = content_groups.total_copies + 1`,
		hash,
	)
	if err != nil {
		return 0, false, fmt.Errorf("failed to upsert content group: %w", err)
	}

	// Fetch the group row to get the id and determine if it was a fresh insert.
	var groupID, totalCopies int64
	err = tx.QueryRowContext(ctx, `SELECT id, total_copies FROM content_groups WHERE content_hash = ?`, hash).
		Scan(&groupID, &totalCopies)
	if err != nil {
		return 0, false, fmt.Errorf("failed to fetch content group: %w", err)
	}
	return groupID, totalCopies == 1, nil
}

// addMemberToGroup adds a file to a content group
func (h *Hasher) addMemberToGroup(ctx context.Context, tx *sql.Tx, groupID, fileID int64) error {
	_, err := tx.ExecContext(ctx, ` INSERT INTO content_group_members (group_id, file_id, is_master, metadata_score)
		VALUES (?, ?, 0, 0)ON CONFLICT (group_id, file_id) DO NOTHING`,
		groupID, fileID,
	)
	return err
}

// HashAll hashes every file in the slice concurrently, bounded by workers.
// Each call is independent — the semaphore limits how many goroutines are
// active at the same time to avoid exhausting file descriptors or memory.
func (h *Hasher) HashAll(ctx context.Context, files []FileRecord, workers int) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for _, f := range files {
		// Respect cancellation before launching work
		select {
		case <-ctx.Done():
			goto done
		default:
		}

		wg.Add(1)
		sem <- struct{}{} // acquire slot
		go func(id int64, path string) {
			defer wg.Done()
			defer func() { <-sem }() // release slot

			if err := h.ProcessFile(ctx, id, path); err != nil {
				h.log.Warn("HashAll: failed to process file", "file_id", id, "error", err)
			}
		}(f.ID, f.AbsPath)
	}

done:
	wg.Wait()
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
