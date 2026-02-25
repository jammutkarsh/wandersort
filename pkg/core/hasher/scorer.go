package hasher

import (
	"context"
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Pre-compiled date patterns for filename matching.
var datePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\d{8}`),             // 20230520
	regexp.MustCompile(`\d{4}-\d{2}-\d{2}`), // 2023-05-20
	regexp.MustCompile(`\d{4}_\d{2}_\d{2}`), // 2023_05_20
}

// genericDirNames is the set of directory names considered meaningless for scoring.
var genericDirNames = map[string]bool{
	"new folder": true, "untitled": true, "dcim": true, "camera": true, "photos": true,
	"videos": true, "backup": true, "old": true, "misc": true, "temp": true, "downloads": true,
	"desktop": true, "documents": true, "pictures": true, "camera roll": true,
}

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

// TODO: implement scoring once metadata extraction is in place:
func (s *Scorer) CalculateScore(_ context.Context, _ int64) (int, error) {
	return 0, nil
}
