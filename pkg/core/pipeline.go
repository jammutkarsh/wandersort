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
func (p *Pipeline) SubmitScan(_ context.Context, rootPaths []string) (uuid.UUID, error) {
	select {
	case <-p.ctx.Done():
		return uuid.Nil, context.Canceled
	default:
	}

	// Intentionally decouple long-running scan lifecycle from HTTP request
	// cancellation: once accepted, the background session lives under pipeline ctx.
	sessionID, expandedRoots, err := p.scanner.PrepareSession(p.ctx, rootPaths)
	if err != nil {
		return uuid.Nil, err
	}

	p.wg.Go(func() {
		p.runSession(sessionID, rootPaths, expandedRoots)
	})

	return sessionID, nil
}

// runSession executes the three sequential phases for a single scan session.
func (p *Pipeline) runSession(sessionID uuid.UUID, originalRoots, expandedRoots []string) {
	finalStatus := scanner.ScanStatusScore
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
	if err := p.scanner.SetSessionStatus(p.ctx, sessionID, scanner.ScanStatusScan); err != nil {
		msg := fmt.Sprintf("failed to set SCAN status: %v", err)
		finalStatus = scanner.ScanStatusFailed
		finalErr = &msg
		return
	}

	p.log.Info("Phase 1/3: Scanning all paths", "session_id", sessionID, "root_count", len(expandedRoots))
	totalFiles := 0
	var firstScanErr error

	for i, expanded := range expandedRoots {
		original := originalRoots[i]

		// Check context before starting each root
		select {
		case <-p.ctx.Done():
			p.log.Info("Pipeline cancelled during scan phase", "session_id", sessionID)
			msg := "pipeline cancelled during scan phase"
			finalStatus = scanner.ScanStatusCancelled
			finalErr = &msg
			return
		default:
		}

		discoveredChan, err := p.scanner.ScanSinglePath(p.ctx, sessionID, expanded, original)
		if err != nil {
			p.log.Error("Failed to scan path", "session_id", sessionID, "path", expanded, "error", err)
			if firstScanErr == nil {
				firstScanErr = fmt.Errorf("scan failed for %s: %w", expanded, err)
			}
			p.scanner.MarkJobComplete(p.ctx, sessionID, expanded)
			continue
		}

		// Drain discovered records; DB upserts happen in scanner.
		for range discoveredChan {
			totalFiles++
		}

		p.scanner.MarkJobComplete(p.ctx, sessionID, expanded)
		p.log.Info("Scanned path", "session_id", sessionID, "path", expanded)
	}

	p.log.Info("Phase 1 complete: all paths scanned",
		"session_id", sessionID,
		"total_files_collected", totalFiles,
	)

	if firstScanErr != nil {
		msg := firstScanErr.Error()
		finalStatus = scanner.ScanStatusFailed
		finalErr = &msg
		return
	}

	// ─── Phase 2: Hash All ──────────────────────────────────────────────
	if err := p.scanner.SetSessionStatus(p.ctx, sessionID, scanner.ScanStatusHash); err != nil {
		msg := fmt.Sprintf("failed to set HASH status: %v", err)
		finalStatus = scanner.ScanStatusFailed
		finalErr = &msg
		return
	}
	p.log.Info("Phase 2/3: Hashing all files", "session_id", sessionID)

	hashed, err := p.hashSessionFiles(p.ctx, sessionID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			msg := "pipeline cancelled during hash phase"
			finalStatus = scanner.ScanStatusCancelled
			finalErr = &msg
		} else {
			msg := fmt.Sprintf("hash phase failed: %v", err)
			finalStatus = scanner.ScanStatusFailed
			finalErr = &msg
		}
		return
	}
	p.log.Info("Phase 2 complete: all files hashed", "session_id", sessionID, "file_count", hashed)

	// ─── Phase 3: Score All ─────────────────────────────────────────────
	if err := p.scanner.SetSessionStatus(p.ctx, sessionID, scanner.ScanStatusScore); err != nil {
		msg := fmt.Sprintf("failed to set SCORE status: %v", err)
		finalStatus = scanner.ScanStatusFailed
		finalErr = &msg
		return
	}
	p.log.Info("Phase 3/3: Scoring all groups", "session_id", sessionID)

	p.hasher.ScoreAll(p.ctx, p.workers)

	p.log.Info("Phase 3 complete: scoring done", "session_id", sessionID)
}

func (p *Pipeline) hashSessionFiles(ctx context.Context, sessionID uuid.UUID) (int, error) {
	const pageSize = 1000
	var lastID int64
	var total int

	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}

		rows, err := p.db.QueryContext(ctx, `
			SELECT id, file_path, source_root, path_type
			FROM file_registry
			WHERE scan_session_id = ? AND id > ?
			  AND scan_status NOT IN ('HASHED', 'ANALYZED', 'ANALYZING')
			ORDER BY id
			LIMIT ?
		`, sessionID.String(), lastID, pageSize)
		if err != nil {
			return total, fmt.Errorf("query hash batch: %w", err)
		}

		batch := make([]hasher.FileRecord, 0, pageSize)
		for rows.Next() {
			var (
				id         int64
				filePath   string
				sourceRoot string
				pathType   string
			)
			if err := rows.Scan(&id, &filePath, &sourceRoot, &pathType); err != nil {
				rows.Close()
				return total, fmt.Errorf("scan hash batch row: %w", err)
			}

			absPath := filePath
			if pathType != scanner.PathTypeAbsolute {
				absPath = scanner.ResolveAbsolute(filePath, sourceRoot)
			}

			batch = append(batch, hasher.FileRecord{ID: id, AbsPath: absPath})
			lastID = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, fmt.Errorf("iterate hash batch rows: %w", err)
		}

		if len(batch) == 0 {
			break
		}

		p.hasher.HashAll(ctx, sessionID, batch, p.workers)
		total += len(batch)

		if len(batch) < pageSize {
			break
		}
	}

	return total, nil
}

// CleanupOrganizedFiles is a pass-through to the scanner's cleanup method.
func (p *Pipeline) CleanupOrganizedFiles(ctx context.Context) (int64, error) {
	return p.scanner.CleanupOrganizedFiles(ctx)
}
