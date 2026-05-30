package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/hasher"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/statusmanager"
)

// Workflow orchestrates scan, hash and score workflow for each session.
// Scanning and hashing run in bounded batches to keep memory usage stable on
// very large roots.
type Workflow struct {
	ctx        context.Context
	db         *db.DB
	outputPath string
	log        logger.Logger

	// Utilities
	path      *path.Resolver
	statusMgr *statusmanager.StatusManager

	// Pipeline components
	scanner *scanner.Scanner
	hasher  *hasher.Hasher

	// Concurreny settings
	workers        int
	wg             sync.WaitGroup // tracks in-flight runSession(s)
	activeSessions sync.Map       // map[uuid.UUID]*status.SessionTracker
}

// NewWorkflow creates a new workflow instance.
func NewWorkflow(ctx context.Context, db *db.DB, log logger.Logger, cfg *config.Configuration) *Workflow {
	sm := statusmanager.NewStatusManager()
	sc := scanner.NewScanner(db, log)
	h := hasher.NewHasher(ctx, db, log, sm)

	return &Workflow{
		ctx:        ctx,
		db:         db,
		scanner:    sc,
		hasher:     h,
		statusMgr:  sm,
		log:        log,
		workers:    cfg.Workers,
		path:       path.New(),
		outputPath: cfg.OutputPath,
	}
}

// SubmitScan creates a new scan session and kicks off the three-phase
// workflow in a background goroutine.
func (p *Workflow) SubmitScan(paths []string) (uuid.UUID, error) {
	select {
	case <-p.ctx.Done():
		return uuid.Nil, context.Canceled
	default:
	}

	sessionID, path, tracker, err := p.prepareSession(p.ctx, paths)
	if err != nil {
		return uuid.Nil, err
	}

	p.activeSessions.Store(sessionID, tracker)

	p.wg.Go(func() {
		p.runSession(sessionID, tracker, path)
	})

	return sessionID, nil
}

// prepareSession creates the scan_sessions DB row, and returns a fresh tracker.
func (p *Workflow) prepareSession(ctx context.Context, paths []string) (uuid.UUID, []string, *statusmanager.SessionTracker, error) {
	normalizedPaths := make([]string, 0, len(paths))
	// Convert to absolute paths and contract home dir for readability in DB and logs
	for _, path := range paths {
		cleanPath := filepath.Clean(path)
		if filepath.IsAbs(cleanPath) {
			normalizedPaths = append(normalizedPaths, p.path.ContractPath(cleanPath))
			continue
		}
		normalizedPaths = append(normalizedPaths, cleanPath)
	}

	p.log.Info("Preparing scan session", "paths", normalizedPaths)

	// Validate all paths before creating the session to fail fast on invalid input.
	for _, path := range normalizedPaths {
		if ok, err := p.path.IsDirectory(path); err != nil || !ok {
			p.log.Error("invalid path, not a directory", "path", path, "error", err)
			return uuid.Nil, nil, nil, fmt.Errorf("path is not a directory: %s", path)
		}
	}

	p.log.Info("All paths validated successfully", "paths", normalizedPaths)

	// Create scan session
	sessionID, _ := uuid.NewV7()
	// fallback to any UUID if V7 generation fails
	if sessionID == uuid.Nil {
		sessionID = uuid.New()
	}
	startedAt := time.Now().UTC()
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO scan_sessions (id, started_at, status, root_paths)
		VALUES (?, ?, ?, ?)
	`, sessionID, startedAt.Format(time.RFC3339), statusmanager.WorkflowStatusStarted, strings.Join(normalizedPaths, ","))
	if err != nil {
		return uuid.Nil, nil, nil, fmt.Errorf("failed to create scan session: %w", err)
	}

	p.log.Info("Scan session created", "session_id", sessionID, "root_paths", normalizedPaths)

	// Initialize the shared state tracker for this session
	progressCtx, cancelProgress := context.WithCancel(ctx)
	tracker := &statusmanager.SessionTracker{
		SessionID: sessionID,
		Ctx:       progressCtx,
		Cancel:    cancelProgress,
	}
	tracker.Status.Store(statusmanager.WorkflowStatusStarted)
	tracker.PendingJobs.Store(int32(len(normalizedPaths)))

	// Start the periodic progress updater for this session
	go p.updateProgress(progressCtx, sessionID, tracker)

	return sessionID, normalizedPaths, tracker, nil
}

// runSession executes the three sequential phases for a single scan session.
func (p *Workflow) runSession(sessionID uuid.UUID, tracker *statusmanager.SessionTracker, paths []string) {
	var finalStatus string
	var finalErr *string

	defer func() {
		p.finalizeSession(sessionID, finalStatus, finalErr)
	}()

	p.log.Info("Pipeline session started", "session_id", sessionID, "phases", "scan → hash → score")

	// ─── Phase 1: Scan All ──────────────────────────────────────────────
	totalFiles, err := p.scanner.RunPhase(p.ctx, sessionID, tracker, paths)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			msg := "pipeline cancelled during scan phase"
			finalStatus = statusmanager.WorkflowStatusCancelled
			finalErr = &msg
		} else {
			msg := err.Error()
			finalStatus = statusmanager.WorkflowStatusFail
			finalErr = &msg
		}
		return
	}
	p.writeUnsupportedFiles(sessionID, tracker)
	p.log.Info("Phase 1 complete: all paths scanned", "session_id", sessionID, "total_files_collected", totalFiles)

	// ─── Phase 2: Hash All ──────────────────────────────────────────────
	hashed, err := p.runHashPhase(sessionID, paths, tracker)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			msg := "pipeline cancelled during hash phase"
			finalStatus = statusmanager.WorkflowStatusCancelled
			finalErr = &msg
		} else {
			msg := fmt.Sprintf("hash phase failed: %v", err)
			finalStatus = statusmanager.WorkflowStatusFail
			finalErr = &msg
		}
		return
	}
	p.log.Info("Phase 2 complete: all files hashed", "session_id", sessionID, "file_count", hashed)

	// ─── Phase 3: Score All ─────────────────────────────────────────────
	if err := p.runScorePhase(sessionID, paths, tracker); err != nil {
		msg := fmt.Sprintf("failed to set SCORE status: %v", err)
		finalStatus = statusmanager.WorkflowStatusFail
		finalErr = &msg
		return
	}
	p.log.Info("Phase 3 complete: scoring done", "session_id", sessionID)
}

func (p *Workflow) runHashPhase(sessionID uuid.UUID, paths []string, tracker *statusmanager.SessionTracker) (int, error) {
	tracker.Status.Store(statusmanager.WorkflowStatusHash)
	if err := p.setSessionStatus(p.ctx, sessionID, statusmanager.WorkflowStatusHash); err != nil {
		return 0, fmt.Errorf("failed to set HASH status: %w", err)
	}
	p.log.Info("Phase 2/3: Hashing all files", "session_id", sessionID)

	return p.hasher.HashPaths(p.ctx, sessionID, paths, p.phaseWorkerCount(paths), p.workers, tracker)
}

func (p *Workflow) runScorePhase(sessionID uuid.UUID, paths []string, tracker *statusmanager.SessionTracker) error {
	tracker.Status.Store(statusmanager.WorkflowStatusScore)
	if err := p.setSessionStatus(p.ctx, sessionID, statusmanager.WorkflowStatusScore); err != nil {
		return err
	}
	p.log.Info("Phase 3/3: Scoring all groups", "session_id", sessionID)

	_, err := p.hasher.ScorePaths(p.ctx, sessionID, paths, p.phaseWorkerCount(paths))
	return err
}

func (p *Workflow) phaseWorkerCount(paths []string) int {
	workerCount := max(len(paths), p.workers)
	if workerCount <= 0 {
		return 1
	}
	return workerCount
}
