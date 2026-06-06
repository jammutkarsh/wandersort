package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/hasher"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/core/scorer"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	sm "github.com/jammutkarsh/wandersort/pkg/statusmanager"
)

// Workflow orchestrates scan, hash and score workflow for each session.
// Scanning and hashing run in bounded batches to keep memory usage stable on
// very large roots.
type Workflow struct {
	ctx        context.Context
	db         *db.DB
	outputPath string
	log        logger.Logger

	/* Utilities */
	path      *path.Resolver
	statusMgr *sm.StatusManager

	/* Pipeline components */
	scanner *scanner.Scanner
	hasher  *hasher.Hasher
	scorer  *scorer.Scorer

	/* Concurreny settings */
	// For a session at any given time,
	// only one phase runs (scan OR hash OR score)
	// and it uses up to this many workers.
	workers int

	updateInterval time.Duration
	finalTimeout   time.Duration

	// tracks in-flight runSession(s)
	wg sync.WaitGroup

	// map[uuid.UUID]*status.SessionTracker
	activeSessions sync.Map
}

type workflowPhase struct {
	kind      workflowPhaseKind
	run       func() (int, error)
	onSuccess func(count int)
}

type workflowPhaseKind string

const (
	workflowPhaseScan workflowPhaseKind = "scan"
	workflowPhaseHash workflowPhaseKind = "hash"
)

func (kind workflowPhaseKind) inProgressStatus() string {
	switch kind {
	case workflowPhaseScan:
		return sm.WorkflowStatusScanning
	case workflowPhaseHash:
		return sm.WorkflowStatusHashing
	default:
		return sm.WorkflowStatusFailed
	}
}

func (kind workflowPhaseKind) completedStatus() string {
	switch kind {
	case workflowPhaseScan:
		return sm.WorkflowStatusScanned
	case workflowPhaseHash:
		return sm.WorkflowStatusHashed
	default:
		return sm.WorkflowStatusFailed
	}
}

// NewWorkflow creates a new workflow instance.
func NewWorkflow(ctx context.Context, db *db.DB, log logger.Logger, cfg *config.Configuration) *Workflow {
	sm := sm.NewStatusManager()
	sc := scanner.NewScanner(db, log)
	h := hasher.NewHasher(ctx, db, log)
	s := scorer.NewScorer(db, log)
	p := path.New()

	return &Workflow{
		ctx:            ctx,
		db:             db,
		scanner:        sc,
		hasher:         h,
		scorer:         s,
		statusMgr:      sm,
		log:            log,
		workers:        cfg.Workers,
		path:           p,
		outputPath:     cfg.OutputPath,
		updateInterval: cfg.UpdateInterval,
		finalTimeout:   cfg.FinalizeTimeout,
	}
}

// SubmitScan creates a new scan session and kicks off the workflow
// workflow in a background goroutine.
func (wf *Workflow) SubmitScan(paths []string) (uuid.UUID, error) {
	select {
	case <-wf.ctx.Done():
		return uuid.Nil, context.Canceled
	default:
	}

	tracker, err := wf.prepareSession(wf.ctx, paths)
	if err != nil {
		return uuid.Nil, err
	}

	wf.activeSessions.Store(tracker.SessionID, tracker)

	wf.wg.Go(func() {
		wf.background(tracker, paths)
	})

	return tracker.SessionID, nil
}

// prepareSession creates the scan_sessions DB row and returns a fresh tracker.
//
// The incoming paths are expected to already be canonical, validated scan roots.
// API-level preparation resolves, deduplicates, and prunes overlapping paths
// before this method runs.
func (wf *Workflow) prepareSession(ctx context.Context, paths []string) (*sm.Tracker, error) {
	storedPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		storedPaths = append(storedPaths, wf.path.RelativeToHome(path))
	}

	wf.log.Info("Preparing scan session", "paths", storedPaths)

	// Create scan session
	sessionID, _ := uuid.NewV7()
	// fallback to any UUID if V7 generation fails
	if sessionID == uuid.Nil {
		sessionID = uuid.New()
	}
	startedAt := time.Now().UTC()
	_, err := wf.db.ExecContext(ctx, `
		INSERT INTO scan_sessions (id, started_at, status, root_paths)
		VALUES (?, ?, ?, ?)
	`, sessionID, startedAt.Format(time.RFC3339), sm.WorkflowStatusStarted, strings.Join(storedPaths, ","))
	if err != nil {
		return nil, fmt.Errorf("failed to create scan session: %w", err)
	}

	wf.log.Info("Scan session created", "session_id", sessionID, "root_paths", storedPaths)

	// Initialize the shared state tracker for this session
	progressCtx, cancelProgress := context.WithCancel(ctx)
	tracker := &sm.Tracker{
		SessionID: sessionID,
		Ctx:       progressCtx,
		Cancel:    cancelProgress,
	}
	tracker.UnsupportedPaths.Store("")
	wf.publishStatus(tracker, sm.WorkflowStatusStarted, nil)

	// Start the periodic progress updater for this session
	go wf.updateProgress(progressCtx, sessionID, tracker)

	return tracker, nil
}

// background executes the three sequential phases for a single scan session.
func (wf *Workflow) background(tracker *sm.Tracker, paths []string) {
	var finalStatus string
	var finalErr *string
	workers := wf.workerCount(paths)

	defer func() {
		wf.finalizeSession(tracker.SessionID, finalStatus, finalErr)
	}()

	wf.log.Info("Workflow session started", "session_id", tracker.SessionID, "phases", "scanning → hashing")

	phases := wf.workflowPhases(tracker, paths, workers)

	for _, phase := range phases {
		count, status, errStr, ok := wf.run(tracker, phase.kind, phase.run)
		finalStatus, finalErr = status, errStr
		if !ok {
			return
		}
		if phase.onSuccess != nil {
			phase.onSuccess(count)
		}
	}

	if err := wf.setSessionStatus(wf.ctx, tracker.SessionID, sm.WorkflowStatusCompleted); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", sm.WorkflowStatusCompleted, err).Error()
		finalStatus = sm.WorkflowStatusFailed
		finalErr = &msg
		return
	}
	finalStatus = sm.WorkflowStatusCompleted
}

func (wf *Workflow) workflowPhases(tracker *sm.Tracker, paths []string, workers int) []workflowPhase {
	return []workflowPhase{
		{
			kind: workflowPhaseScan,
			run: func() (int, error) {
				return wf.scanner.Run(wf.ctx, tracker, paths, workers)
			},
			onSuccess: func(count int) {
				wf.writeUnsupportedFiles(tracker)
				wf.log.Info("Phase 1 complete: all paths scanned", "session_id", tracker.SessionID, "files_collected", count)
			},
		},
		{
			kind: workflowPhaseHash,
			run: func() (int, error) {
				return wf.hasher.Run(wf.ctx, tracker, workers)
			},
			onSuccess: func(count int) {
				wf.log.Info("Phase 2 complete: all files hashed", "session_id", tracker.SessionID, "files_hashed", count)
			},
		},
	}
}

// run runs a single workflow phase, handles logging,
// status updates, and consistent error reporting. Returns the result count,
// final status, error message (if any), and a boolean indicating success.
func (wf *Workflow) run(tracker *sm.Tracker, phase workflowPhaseKind, phaseFunc func() (int, error)) (int, string, *string, bool) {
	success := true
	inProgressStatus := phase.inProgressStatus()
	if err := wf.setSessionStatus(wf.ctx, tracker.SessionID, inProgressStatus); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", inProgressStatus, err).Error()
		return 0, sm.WorkflowStatusFailed, &msg, !success
	}

	wf.log.Info("Starting phase", "session_id", tracker.SessionID, "phase", inProgressStatus)
	count, err := phaseFunc()
	if err != nil {
		var finalStatus string
		var finalErr string
		if errors.Is(err, context.Canceled) {
			finalStatus = sm.WorkflowStatusCancelled
			finalErr = fmt.Sprintf("pipeline cancelled during %s phase", inProgressStatus)
		} else {
			finalStatus = sm.WorkflowStatusFailed
			finalErr = fmt.Sprintf("%s phase failed: %v", inProgressStatus, err)
		}
		return count, finalStatus, &finalErr, !success
	}

	completedStatus := phase.completedStatus()
	if err := wf.setSessionStatus(wf.ctx, tracker.SessionID, completedStatus); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", completedStatus, err).Error()
		return count, sm.WorkflowStatusFailed, &msg, !success
	}

	return count, completedStatus, nil, success
}

func (wf *Workflow) workerCount(paths []string) int {
	return max(len(paths), wf.workers)
}
