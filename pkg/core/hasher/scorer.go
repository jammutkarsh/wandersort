package hasher

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Scorer handles master file selection within content groups
type Scorer struct {
	db  *pgxpool.Pool
	log logger.Logger
}

// NewScorer creates a new scorer instance
func NewScorer(db *pgxpool.Pool, log logger.Logger) *Scorer {
	return &Scorer{
		db:  db,
		log: log,
	}
}

// CalculateScore computes a score for a file based on metadata quality.
//
// TODO: implement scoring once metadata extraction is in place:
//   - +10 if file has EXIF metadata (requires file_metadata table to be populated)
//   - +5  if filename matches a date pattern (see hasDatePattern)
//   - +2  if parent directory has a meaningful name (see hasMeaningfulDir)
func (s *Scorer) CalculateScore(_ context.Context, _ int64) (int, error) {
	return 0, nil
}

// hasDatePattern checks if filename contains a date pattern.
//
// TODO: patterns are incomplete — does not yet handle all common camera naming
// conventions (e.g. VID_YYYYMMDD, DSC_YYYYMMDD, WhatsApp Image YYYY-MM-DD).
// Also needs validation that the extracted digits form a plausible calendar date,
// not just any 8-digit sequence.
func (s *Scorer) hasDatePattern(filename string) bool {
	// Patterns: 20230520, 2023-05-20, 2023_05_20, IMG_20230520, etc.
	patterns := []string{
		`\d{8}`,             // 20230520
		`\d{4}-\d{2}-\d{2}`, // 2023-05-20
		`\d{4}_\d{2}_\d{2}`, // 2023_05_20
	}

	for _, pattern := range patterns {
		matched, _ := regexp.MatchString(pattern, filename)
		if matched {
			return true
		}
	}

	return false
}

// hasMeaningfulDir checks if the parent directory name is meaningful.
//
// TODO: the deny-list approach is fragile and locale-specific. Consider
// replacing with an allow-list of known meaningful patterns (year folders,
// event names, etc.) or a scored heuristic once more use-cases are understood.
func (s *Scorer) hasMeaningfulDir(filePath string) bool {
	dir := filepath.Dir(filePath)
	dirName := strings.ToLower(filepath.Base(dir))

	// Generic/meaningless folder names
	genericNames := []string{
		"new folder", "untitled", "dcim", "camera", "photos",
		"videos", "backup", "old", "misc", "temp", "downloads",
		"desktop", "documents", "pictures", "camera roll",
	}

	for _, generic := range genericNames {
		if strings.Contains(dirName, generic) {
			return false
		}
	}

	return true
}

// ElectMaster selects the best file in a content group to be the master.
// Selection is deterministic: highest CalculateScore wins; ties broken by
// alphabetically-earliest file path.
//
// TODO: not called yet — blocked on CalculateScore being fully implemented.
// Uncomment the call in ScoreAndElectAllMasters once scoring is ready.
/*
func (s *Scorer) ElectMaster(ctx context.Context, groupID int64) error {
	rows, err := s.db.Query(ctx, `
		SELECT cgm.id, cgm.file_id, fr.file_path
		FROM content_group_members cgm
		JOIN file_registry fr ON fr.id = cgm.file_id
		WHERE cgm.group_id = $1
	`, groupID)
	if err != nil {
		return fmt.Errorf("failed to get group members: %w", err)
	}
	defer rows.Close()

	type member struct {
		ID     int64
		FileID int64
		Path   string
		Score  int
	}
	var members []member

	for rows.Next() {
		var m member
		if err := rows.Scan(&m.ID, &m.FileID, &m.Path); err != nil {
			return fmt.Errorf("failed to scan member: %w", err)
		}
		score, err := s.CalculateScore(ctx, m.FileID)
		if err != nil {
			s.log.Warn("Failed to calculate score, using 0", "file_id", m.FileID, "error", err)
			score = 0
		}
		m.Score = score
		_, err = s.db.Exec(ctx, `
			UPDATE content_group_members SET metadata_score = $1 WHERE id = $2
		`, score, m.ID)
		if err != nil {
			return fmt.Errorf("failed to update score: %w", err)
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("row iteration error for group %d: %w", groupID, err)
	}
	if len(members) == 0 {
		return fmt.Errorf("no members found in group %d", groupID)
	}

	// Deterministic selection: score DESC, then path ASC
	masterID := members[0].FileID
	maxScore := members[0].Score
	masterPath := members[0].Path
	for _, m := range members[1:] {
		if m.Score > maxScore || (m.Score == maxScore && m.Path < masterPath) {
			masterID = m.FileID
			maxScore = m.Score
			masterPath = m.Path
		}
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `UPDATE content_group_members SET is_master = FALSE WHERE group_id = $1`, groupID)
	if err != nil {
		return fmt.Errorf("failed to reset masters: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE content_group_members SET is_master = TRUE WHERE group_id = $1 AND file_id = $2`, groupID, masterID)
	if err != nil {
		return fmt.Errorf("failed to set master: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE content_groups SET master_file_id = $1 WHERE id = $2`, masterID, groupID)
	if err != nil {
		return fmt.Errorf("failed to update group master: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.log.Debug("Master elected", "group_id", groupID, "master_file_id", masterID, "score", maxScore, "path", masterPath)
	return nil
}
*/

// ScoreAndElectAllMasters processes all content groups and elects a master file
// for each. Intended to run once after all hashing jobs for a session complete.
//
// TODO: not called yet — blocked on ElectMaster (see above). Wire this into the
// job pipeline (e.g. a "finalize" River job) once scoring is ready.
func (s *Scorer) ScoreAndElectAllMasters(_ context.Context) error {
	// TODO: uncomment once ElectMaster is implemented.
	// rows, err := s.db.Query(ctx, `SELECT id FROM content_groups ORDER BY id`)
	// ...
	return nil
}
