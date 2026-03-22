package hasher

import (
	"context"
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/status"
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
	db        *db.DB
	log       logger.Logger
	statusMgr *status.StatusManager
}

type scoreSessionTracker struct {
	sessionID uuid.UUID
	pending   atomic.Int32
}

// NewScorer creates a new scorer instance
func NewScorer(db *db.DB, log logger.Logger) *Scorer {
	return &Scorer{
		db:  db,
		log: log,
	}
}

// ScorePaths fans out scoring work by source root and waits for completion.
// Current scoring is still a stub, but the path-level orchestration exists so
// progress reporting stays consistent with scan and hash phases.
func (s *Scorer) ScorePaths(ctx context.Context, sessionID uuid.UUID, paths []string, workers int) (int, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	if workers <= 0 {
		workers = 1
	}

	tracker := &scoreSessionTracker{sessionID: sessionID}
	tracker.pending.Store(int32(len(paths)))

	type scoreResult struct {
		count int
		err   error
	}

	jobs := make(chan string, len(paths))
	results := make(chan scoreResult, len(paths))

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for path := range jobs {
				count, err := s.ScorePath(ctx, sessionID, path)
				s.markJobComplete(sessionID, path, tracker)
				results <- scoreResult{count: count, err: err}
			}
		})
	}

	for _, path := range paths {
		jobs <- path
	}
	close(jobs)
	wg.Wait()
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

// ScorePath is a placeholder for future metadata scoring scoped to a single
// source root.
func (s *Scorer) ScorePath(ctx context.Context, sessionID uuid.UUID, path string) (int, error) {
	_ = ctx
	s.log.Info("Scoring path", "session_id", sessionID, "path", path)
	return 0, nil
}

func (s *Scorer) CalculateScore(_ context.Context, _ int64) (int, error) {
	return 0, nil
}

func (s *Scorer) markJobComplete(sessionID uuid.UUID, path string, tracker *scoreSessionTracker) {
	pending := tracker.pending.Add(-1)
	s.log.Debug("Score job completed", "session_id", sessionID, "path", path, "pending_jobs_remaining", pending)
	if pending == 0 {
		s.log.Info("All score jobs complete for session", "session_id", sessionID)
		s.broadcastScoreProgress(sessionID)
	}
}

func (s *Scorer) broadcastScoreProgress(sessionID uuid.UUID) {
	if s.statusMgr == nil {
		return
	}

	current := s.statusMgr.GetCurrent()
	if current.SessionID != sessionID {
		current = status.PipelineStatus{SessionID: sessionID}
	}
	current.Status = status.PipelineStatusScore
	s.statusMgr.Broadcast(current)
}
