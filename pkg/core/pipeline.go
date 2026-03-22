package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/hasher"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/status"
)

// Pipeline orchestrates scan, hash and score workflow for each session.
// Scanning and hashing run in bounded batches to keep memory usage stable on
// very large roots.
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
	h := hasher.NewHasher(ctx, db, log, sm)

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
func (p *Pipeline) SubmitScan(paths []string) (uuid.UUID, error) {
	select {
	case <-p.ctx.Done():
		return uuid.Nil, context.Canceled
	default:
	}

	sessionID, path, err := p.scanner.PrepareSession(p.ctx, paths)
	if err != nil {
		return uuid.Nil, err
	}

	p.wg.Go(func() {
		p.runSession(sessionID, path)
	})

	return sessionID, nil
}

// runSession executes the three sequential phases for a single scan session.
func (p *Pipeline) runSession(sessionID uuid.UUID, paths []string) {
	finalStatus := status.PipelineStatusScore
	var finalErr *string

	defer func() {
		finalizeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := p.scanner.FinalizeSession(finalizeCtx, sessionID, finalStatus, finalErr); err != nil {
			p.log.Error("Failed to finalize pipeline session", "session_id", sessionID, "status", finalStatus, "error", err)
		}
		p.log.Info("Pipeline session finished", "session_id", sessionID, "status", finalStatus)
	}()

	p.log.Info("Pipeline session started", "session_id", sessionID, "phases", "scan → hash → score")

	// ─── Phase 1: Scan All ──────────────────────────────────────────────
	totalFiles, err := p.runScanPhase(sessionID, paths)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			msg := "pipeline cancelled during scan phase"
			finalStatus = status.PipelineStatusCancelled
			finalErr = &msg
		} else {
			msg := err.Error()
			finalStatus = status.PipelineStatusFail
			finalErr = &msg
		}
		return
	}
	p.log.Info("Phase 1 complete: all paths scanned", "session_id", sessionID, "total_files_collected", totalFiles)

	// ─── Phase 2: Hash All ──────────────────────────────────────────────
	hashed, err := p.runHashPhase(sessionID, paths)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			msg := "pipeline cancelled during hash phase"
			finalStatus = status.PipelineStatusCancelled
			finalErr = &msg
		} else {
			msg := fmt.Sprintf("hash phase failed: %v", err)
			finalStatus = status.PipelineStatusFail
			finalErr = &msg
		}
		return
	}
	p.log.Info("Phase 2 complete: all files hashed", "session_id", sessionID, "file_count", hashed)

	// ─── Phase 3: Score All ─────────────────────────────────────────────
	if err := p.runScorePhase(sessionID, paths); err != nil {
		msg := fmt.Sprintf("failed to set SCORE status: %v", err)
		finalStatus = status.PipelineStatusFail
		finalErr = &msg
		return
	}
	p.log.Info("Phase 3 complete: scoring done", "session_id", sessionID)
}

func (p *Pipeline) runScanPhase(sessionID uuid.UUID, paths []string) (int, error) {
	if err := p.scanner.SetSessionStatus(p.ctx, sessionID, status.PipelineStatusScan); err != nil {
		return 0, fmt.Errorf("failed to set SCAN status: %w", err)
	}

	p.log.Info("Phase 1/3: Scanning all paths", "session_id", sessionID, "path_count", len(paths))
	type scanResult struct {
		count int
		err   error
	}

	jobs := make(chan string, len(paths))
	results := make(chan scanResult, len(paths))
	workerCount := p.phaseWorkerCount(paths)

	var workers sync.WaitGroup
	for range workerCount {
		workers.Go(func() {
			for path := range jobs {
				count, err := p.scanPath(sessionID, path)
				results <- scanResult{count: count, err: err}
			}
		})
	}

	for _, path := range paths {
		jobs <- path
	}
	close(jobs)
	workers.Wait()
	close(results)

	totalFiles := 0
	var firstScanErr error
	for result := range results {
		totalFiles += result.count
		if result.err != nil && firstScanErr == nil {
			firstScanErr = result.err
		}
	}

	return totalFiles, firstScanErr
}

func (p *Pipeline) runHashPhase(sessionID uuid.UUID, paths []string) (int, error) {
	if err := p.scanner.SetSessionStatus(p.ctx, sessionID, status.PipelineStatusHash); err != nil {
		return 0, fmt.Errorf("failed to set HASH status: %w", err)
	}
	p.log.Info("Phase 2/3: Hashing all files", "session_id", sessionID)

	return p.hasher.HashPaths(p.ctx, sessionID, paths, p.phaseWorkerCount(paths), p.workers)
}

func (p *Pipeline) runScorePhase(sessionID uuid.UUID, paths []string) error {
	if err := p.scanner.SetSessionStatus(p.ctx, sessionID, status.PipelineStatusScore); err != nil {
		return err
	}
	p.log.Info("Phase 3/3: Scoring all groups", "session_id", sessionID)

	_, err := p.hasher.ScorePaths(p.ctx, sessionID, paths, p.phaseWorkerCount(paths))
	return err
}

func (p *Pipeline) phaseWorkerCount(paths []string) int {
	workerCount := max(len(paths), p.workers)
	if workerCount <= 0 {
		return 1
	}
	return workerCount
}

func (p *Pipeline) scanPath(sessionID uuid.UUID, path string) (int, error) {
	select {
	case <-p.ctx.Done():
		p.log.Info("Pipeline cancelled during scan phase", "session_id", sessionID)
		return 0, p.ctx.Err()
	default:
	}

	discoveredChan, err := p.scanner.ScanSinglePath(p.ctx, sessionID, path)
	if err != nil {
		p.log.Error("Failed to scan path", "session_id", sessionID, "path", path, "error", err)
		p.scanner.MarkJobComplete(p.ctx, sessionID, path)
		return 0, fmt.Errorf("scan failed for %s: %w", path, err)
	}

	count := 0
	for range discoveredChan {
		count++
	}

	p.scanner.MarkJobComplete(p.ctx, sessionID, path)
	p.log.Info("Scanned path", "session_id", sessionID, "path", path, "files_discovered", count)
	return count, nil
}
