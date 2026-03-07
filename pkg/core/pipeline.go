package core

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/hasher"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/status"
)

// Pipeline orchestrates the sequential scan-then-hash-then-score workflow.
// When a scan is submitted, a background goroutine runs three phases in order:
//
//  1. Scan All  — walk every root path and collect discovered file records
//  2. Hash All  — fan out BLAKE3 hashing across N workers
//  3. Score All — fan out scoring across N workers (stub today)
type Pipeline struct {
	ctx       context.Context
	db        *db.DB
	scanner   *scanner.Scanner
	hasher    *hasher.Hasher
	statusMgr *status.StatusManager
	log       logger.Logger
	workers   int
	wg        sync.WaitGroup // tracks in-flight runSession goroutines
}

// NewPipeline creates a new pipeline instance.
func NewPipeline(ctx context.Context, db *db.DB, log logger.Logger, cfg *config.Configuration) *Pipeline {
	sm := status.NewStatusManager()
	sc := scanner.NewScanner(db, log, cfg.OutputPath, sm)
	h := hasher.NewHasher(db, log, sm)

	return &Pipeline{
		ctx:       ctx,
		db:        db,
		scanner:   sc,
		hasher:    h,
		statusMgr: sm,
		log:       log,
		workers:   cfg.Workers,
	}
}

// Scanner returns the underlying scanner for use by the API layer.
func (p *Pipeline) Scanner() *scanner.Scanner {
	return p.scanner
}

// StatusStream returns a new channel subscribed to pipeline progress updates.
func (p *Pipeline) StatusStream() chan status.PipelineStatus {
	return p.statusMgr.Subscribe()
}

// UnsubscribeStatus removes a subscriber from progress updates.
func (p *Pipeline) UnsubscribeStatus(ch chan status.PipelineStatus) {
	p.statusMgr.Unsubscribe(ch)
}

// Close gracefully waits for all in-flight sessions to finish.
// Call this before closing the database to prevent panics.
func (p *Pipeline) Close() {
	p.wg.Wait()
}

// SubmitScan creates a new scan session and kicks off the three-phase
// pipeline in a background goroutine.
func (p *Pipeline) SubmitScan(ctx context.Context, rootPaths []string) (uuid.UUID, error) {
	sessionID, expandedRoots, err := p.scanner.PrepareSession(ctx, rootPaths)
	if err != nil {
		return uuid.Nil, err
	}

	p.wg.Go(func() {
		p.runSession(sessionID, rootPaths, expandedRoots)
	})

	return sessionID, nil
}

// FileRecord is the minimal info needed from the scan phase
// to drive the hash phase.

// runSession executes the three sequential phases for a single scan session.
func (p *Pipeline) runSession(sessionID uuid.UUID, originalRoots, expandedRoots []string) {
	p.log.Info("Pipeline session started", "session_id", sessionID, "phases", "scan → hash → score")

	// ─── Phase 1: Scan All ──────────────────────────────────────────────
	p.log.Info("Phase 1/3: Scanning all paths", "session_id", sessionID, "root_count", len(expandedRoots))

	var allFiles []hasher.FileRecord

	for i, expanded := range expandedRoots {
		original := originalRoots[i]

		// Check context before starting each root
		select {
		case <-p.ctx.Done():
			p.log.Info("Pipeline cancelled during scan phase", "session_id", sessionID)
			return
		default:
		}

		discoveredChan, err := p.scanner.ScanSinglePath(p.ctx, sessionID, expanded, original)
		if err != nil {
			p.log.Error("Failed to scan path", "session_id", sessionID, "path", expanded, "error", err)
			continue
		}

		// Drain the channel — all DB upserts happen inside the scanner
		for f := range discoveredChan {
			absPath := filepath.Join(expanded, f.Path)
			allFiles = append(allFiles, hasher.FileRecord{ID: f.ID, AbsPath: absPath})
		}

		p.scanner.MarkJobComplete(p.ctx, sessionID, expanded)
		p.log.Info("Scanned path", "session_id", sessionID, "path", expanded)
	}

	p.log.Info("Phase 1 complete: all paths scanned",
		"session_id", sessionID,
		"total_files_collected", len(allFiles),
	)

	// ─── Phase 2: Hash All ──────────────────────────────────────────────
	p.log.Info("Phase 2/3: Hashing all files", "session_id", sessionID, "file_count", len(allFiles))

	p.hasher.HashAll(p.ctx, allFiles, p.workers)

	p.log.Info("Phase 2 complete: all files hashed", "session_id", sessionID)

	// ─── Phase 3: Score All ─────────────────────────────────────────────
	p.log.Info("Phase 3/3: Scoring all groups", "session_id", sessionID)

	p.hasher.ScoreAll(p.ctx, p.workers)

	p.log.Info("Phase 3 complete: scoring done", "session_id", sessionID)
	p.log.Info("Pipeline session finished", "session_id", sessionID)
}

// CleanupOrganizedFiles is a pass-through to the scanner's cleanup method.
func (p *Pipeline) CleanupOrganizedFiles(ctx context.Context) (int64, error) {
	return p.scanner.CleanupOrganizedFiles(ctx)
}
