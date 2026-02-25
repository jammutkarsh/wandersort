package hasher

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"lukechampine.com/blake3"
)

// Hasher handles file hashing and content group management
type Hasher struct {
	db     *pgxpool.Pool
	log    logger.Logger
	scorer *Scorer
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

	// Create or get content group
	groupID, isNew, err := h.createOrGetGroup(ctx, hash)
	if err != nil {
		return fmt.Errorf("failed to create/get content group: %w", err)
	}

	// Add file to group
	if err := h.addMemberToGroup(ctx, groupID, fileID); err != nil {
		return fmt.Errorf("failed to add member to group: %w", err)
	}

	h.log.Debug("File hashed successfully",
		"file_id", fileID,
		"hash", hash[:16]+"...",
		"group_id", groupID,
		"is_new_group", isNew)

	return nil
}

// createOrGetGroup creates a new content group or returns existing one
func (h *Hasher) createOrGetGroup(ctx context.Context, hash string) (int64, bool, error) {
	// Try to get existing group
	var groupID int64
	err := h.db.QueryRow(ctx, `
		SELECT id FROM content_groups WHERE content_hash = $1
	`, hash).Scan(&groupID)

	if err == nil {
		// Group exists, increment total_copies
		_, err = h.db.Exec(ctx, `
			UPDATE content_groups 
			SET total_copies = total_copies + 1 
			WHERE id = $1
		`, groupID)

		if err != nil {
			return 0, false, fmt.Errorf("failed to update total_copies: %w", err)
		}

		return groupID, false, nil
	}

	// Any error other than "not found" is a real DB problem
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("failed to query content group: %w", err)
	}

	// Group doesn't exist, create it
	err = h.db.QueryRow(ctx, `
		INSERT INTO content_groups (content_hash, total_copies)
		VALUES ($1, 1)
		RETURNING id
	`, hash).Scan(&groupID)

	if err != nil {
		return 0, false, fmt.Errorf("failed to create content group: %w", err)
	}

	return groupID, true, nil
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

// GetGroupStats returns statistics about content groups
func (h *Hasher) GetGroupStats(ctx context.Context) (map[string]interface{}, error) {
	var stats struct {
		TotalGroups     int
		GroupsWithDupes int
		TotalFiles      int
		DuplicateFiles  int
		MastersElected  int
	}

	err := h.db.QueryRow(ctx, `
		WITH
			group_stats AS (
				SELECT
					COUNT(*)                                             AS total_groups,
					COUNT(*) FILTER (WHERE total_copies > 1)            AS groups_with_dupes,
					COUNT(*) FILTER (WHERE master_file_id IS NOT NULL)  AS masters_elected
				FROM content_groups
			),
			member_stats AS (
				SELECT
					COUNT(*)                                             AS total_files,
					COUNT(*) FILTER (WHERE is_master = FALSE)           AS duplicate_files
				FROM content_group_members
			)
		SELECT
			total_groups,
			groups_with_dupes,
			masters_elected,
			total_files,
			duplicate_files
		FROM group_stats, member_stats
	`).Scan(
		&stats.TotalGroups,
		&stats.GroupsWithDupes,
		&stats.MastersElected,
		&stats.TotalFiles,
		&stats.DuplicateFiles,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query group stats: %w", err)
	}

	return map[string]any{
		"total_groups":      stats.TotalGroups,
		"groups_with_dupes": stats.GroupsWithDupes,
		"total_files":       stats.TotalFiles,
		"duplicate_files":   stats.DuplicateFiles,
		"masters_elected":   stats.MastersElected,
	}, nil
}
