package hasher

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"lukechampine.com/blake3"
)

// Hasher handles file hashing and content group management
type Hasher struct {
	db          *pgxpool.Pool
	log         logger.Logger
	scorer      *Scorer
	totalHashed atomic.Int64 // lifetime counter across all sessions
}

// NewHasher creates a new hasher instance
func NewHasher(db *pgxpool.Pool, log logger.Logger) *Hasher {
	return &Hasher{
		db:     db,
		log:    log,
		scorer: NewScorer(db, log),
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
	h.log.Info("Hashing file", "file_id", fileID, "path", filePath)

	// Compute hash
	hash, err := h.HashFile(filePath)
	if err != nil {
		h.log.Error("Failed to hash file", "file_id", fileID, "path", filePath, "error", err)

		// Mark file as error in registry
		_, updateErr := h.db.Exec(ctx, `
			UPDATE file_registry 
			SET scan_status = 'ERROR' 
			WHERE id = $1
		`, fileID)

		if updateErr != nil {
			h.log.Error("Failed to mark file as error", "file_id", fileID, "error", updateErr)
		}

		return err
	}

	// Update file registry with hash
	_, err = h.db.Exec(ctx, `
		UPDATE file_registry 
		SET file_hash = $1, scan_status = 'HASHED' 
		WHERE id = $2
	`, hash, fileID)

	if err != nil {
		return fmt.Errorf("failed to update file registry: %w", err)
	}

	// Create or get content group (uses INSERT ON CONFLICT for concurrency safety)
	groupID, isNew, err := h.getOrCreateGroup(ctx, hash)
	if err != nil {
		return fmt.Errorf("failed to create/get content group: %w", err)
	}

	// Add file to group
	if err := h.addMemberToGroup(ctx, groupID, fileID); err != nil {
		return fmt.Errorf("failed to add member to group: %w", err)
	}

	n := h.totalHashed.Add(1)
	h.log.Info("File hashed",
		"file_id", fileID,
		"hash", hash[:16]+"...",
		"group_id", groupID,
		"is_new_group", isNew,
		"total_hashed_lifetime", n)

	// Milestone log every 1000 files so progress is visible without flooding logs
	if n%1000 == 0 {
		h.log.Info("Hashing milestone", "files_hashed", n)
	}

	return nil
}

// getOrCreateGroup atomically creates a new content group or returns the existing one.
// Uses INSERT ... ON CONFLICT to avoid the TOCTOU race that would occur with a
// separate SELECT-then-INSERT approach when multiple hashers process the same hash.
func (h *Hasher) getOrCreateGroup(ctx context.Context, hash string) (int64, bool, error) {
	var groupID int64
	var isNew bool

	err := h.db.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO content_groups (content_hash, total_copies)
			VALUES ($1, 1)
			ON CONFLICT (content_hash) DO UPDATE
				SET total_copies = content_groups.total_copies + 1
			RETURNING id, (xmax = 0) AS is_new
		)
		SELECT id, is_new FROM ins
	`, hash).Scan(&groupID, &isNew)
	if err != nil {
		return 0, false, fmt.Errorf("failed to upsert content group: %w", err)
	}

	return groupID, isNew, nil
}

// addMemberToGroup adds a file to a content group
func (h *Hasher) addMemberToGroup(ctx context.Context, groupID, fileID int64) error {
	_, err := h.db.Exec(ctx, `
		INSERT INTO content_group_members (group_id, file_id, is_master, metadata_score)
		VALUES ($1, $2, FALSE, 0)
		ON CONFLICT (group_id, file_id) DO NOTHING
	`, groupID, fileID)

	return err
}
