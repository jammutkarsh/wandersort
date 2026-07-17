package scorer

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

var (
	// Matches date-prefixed filenames: YYYYMMDD_ or YYYY-MM-DD_ or YYYY_MM_DD_
	// Ref: https://en.wikipedia.org/wiki/ISO_8601#Calendar_dates
	datePattern = regexp.MustCompile(`^(\d{8}_|\d{4}-\d{2}-\d{2}_|\d{4}_\d{2}_\d{2}_)`)
	// Matches camera-generated filenames per DCF spec:
	// IMG_3162, _MG_1721 (Adobe RGB), DSC01234, WP_0001, GOPR1234, ABCD0001, etc.
	// Structure: optional underscore + 1-5 letters + optional underscore + digits.
	// Ref: https://en.wikipedia.org/wiki/Design_rule_for_Camera_File_system
	cameraPattern = regexp.MustCompile(`^(_?[A-Z]{1,5}_?)\d+(_\d+)*$`)

	// Catches copy-artifact suffixes: "IMG_1234 (1)", "Photo - Copy", "Photo copy 2".
	duplicateSuffixPattern = regexp.MustCompile(`(?i)[ _-]*\(\d+\)$|[ _-]copy(\s*\(?\d*\)?)?$`)

	// Possibly human-readable names
	hasLetterPattern = regexp.MustCompile(`[a-zA-Z]`)

	// Known exact low-signal directory names (case-insensitive).
	genericDirs = map[string]bool{
		"dcim": true, "camera": true, "photos": true, "temp": true,
		"downloads": true, "desktop": true, "backup": true, "misc": true,
	}

	// Catches variants a fixed set can't enumerate: "DCIM 1", "Backup_2023",
	// "New folder (2)", "old backup", ".thumbnails", "WhatsApp Images", etc.
	genericDirPattern = regexp.MustCompile(`(?i)^(\.?(thumbnails|trashed.*|sync|cache))$` +
		`|^new folder(\s*\(\d+\))?$` +
		`|^(old\s+)?backup[\s_-]*\d*$` +
		`|^(dcim|temp|tmp|misc|downloads?)[\s_-]*\d*$` +
		`|^(whatsapp|telegram|signal)[\s_-]*(images?|videos?|media)$`)
)

const (
	scoreMeaningful     = 4
	scoreDatePattern    = 3
	scoreDirBonus       = 2
	penaltyDuplicateTag = -3
)

type Scorer struct {
	db  *db.DB
	log logger.Logger
}

func New(db *db.DB, log logger.Logger) *Scorer {
	return &Scorer{db: db, log: log}
}

func (s *Scorer) Run(ctx context.Context, sessionID uuid.UUID) (int, error) {
	s.log.Info("Scoring session", "sessionId", sessionID)

	// Re-promote solo files stuck at is_master = 0: when a re-scan sweeps every
	// other member of a duplicate group, the demoted survivor is no longer in
	// any COUNT(*) > 1 group below, so nothing else would ever elect it again.
	// Soft-deleted files don't count as group members anywhere in this phase
	if _, err := s.db.ExecContext(ctx, `
		UPDATE file_metadata SET is_master = 1
		WHERE is_master = 0 AND file_hash IN (
			SELECT fm.file_hash FROM file_metadata fm
			JOIN file_registry fr ON fr.id = fm.file_id
			WHERE fr.deleted_at IS NULL
			GROUP BY fm.file_hash HAVING COUNT(*) = 1)`); err != nil {
		return 0, fmt.Errorf("re-promote solo masters: %w", err)
	}

	type member struct {
		FileHash string `db:"file_hash"`
		FileID   int64  `db:"file_id"`
		FileDir  string `db:"file_dir"`
		FileName string `db:"file_name"`
	}

	// Ordering by (file_dir, file_name) within each hash makes the election
	// deterministic: ties on score and path length keep the first member seen,
	// which must not depend on AUTOINCREMENT insertion order across re-scans
	var rows []member
	if err := s.db.SQL.SelectContext(ctx, &rows, `
		SELECT fm.file_hash, fm.file_id,
			fr.file_dir, fr.file_name
		FROM file_metadata fm
		JOIN file_registry fr ON fr.id = fm.file_id
		WHERE fr.deleted_at IS NULL AND fm.file_hash IN (
			SELECT fm2.file_hash FROM file_metadata fm2
			JOIN file_registry fr2 ON fr2.id = fm2.file_id
			WHERE fr2.deleted_at IS NULL
			GROUP BY fm2.file_hash HAVING COUNT(*) > 1 )
		ORDER BY fm.file_hash, fr.file_dir, fr.file_name`); err != nil {
		return 0, fmt.Errorf("query members: %w", err)
	}

	count, start := 0, 0
	for start < len(rows) {
		// Sliding window to find the next group of duplicates with the same hash
		end := start
		for end < len(rows) && rows[end].FileHash == rows[start].FileHash {
			end++
		}
		duplicates := rows[start:end]
		start = end
		count++

		bestScore, bestPathLen, master := math.MinInt, math.MaxInt, member{}
		for _, dupe := range duplicates {
			score := perFileScore(filepath.Join(dupe.FileDir, dupe.FileName))
			pathLen := len(dupe.FileDir) + len(dupe.FileName)
			if score > bestScore || (score == bestScore && pathLen < bestPathLen) {
				master = dupe
				bestScore = score
				bestPathLen = pathLen
			}
		}

		if !s.db.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			if _, err := tx.ExecContext(ctx, `
				UPDATE file_metadata
				SET is_master = CASE WHEN file_id = ? THEN 1 ELSE 0 END
				WHERE file_hash = ?`, master.FileID, master.FileHash); err != nil {
				return fmt.Errorf("persist master for hash %s: %w", master.FileHash, err)
			}
			return nil
		}) {
			return count, fmt.Errorf("persist master for hash %s: writer closed", master.FileHash)
		}

		s.log.Debug("Persisted group master", "sessionId", sessionID, "hash", master.FileHash,
			"masterFileId", master.FileID, "memberCount", len(duplicates))
	}

	s.log.Info("Scored duplicate groups", "sessionId", sessionID, "count", count)
	return count, nil
}

// perFileScore computes a metadata quality score for a single file path.
// Signals: human-readable name (+4), date-prefixed name (+3), non-generic
// directory (+2), duplicate-copy suffix like "(1)" or "copy" (-3).
func perFileScore(filePath string) int {
	dir, name := filepath.Split(filePath)
	stem := strings.TrimSuffix(name, filepath.Ext(name))

	score := 0
	if !cameraPattern.MatchString(stem) && hasLetterPattern.MatchString(stem) {
		score += scoreMeaningful
	}
	if datePattern.MatchString(name) {
		score += scoreDatePattern
	}
	if !IsInGenericDir(dir) {
		score += scoreDirBonus
	}
	// Penalize duplicate-copy suffixes, which are common in camera roll imports and cloud syncs.
	if duplicateSuffixPattern.MatchString(stem) {
		score += penaltyDuplicateTag
	}
	return score
}

// IsInGenericDir reports whether any segment of dir is a known or
// pattern-matched low-signal folder name (DCIM, Backup, temp, etc).
func IsInGenericDir(dir string) bool {
	dir = filepath.Clean(dir)
	for dir != "." && dir != "/" && dir != "" {
		seg := strings.ToLower(filepath.Base(dir))
		if genericDirs[seg] || genericDirPattern.MatchString(seg) {
			return true
		}
		dir = filepath.Dir(dir)
	}
	return false
}
