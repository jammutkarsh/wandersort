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
	status    string
	run       func() (int, error)
	onSuccess func(count int)
}

// NewWorkflow creates a new workflow instance.
func NewWorkflow(ctx context.Context, db *db.DB, log logger.Logger, cfg *config.Configuration) *Workflow {
	sm := sm.NewStatusManager()
	sc := scanner.NewScanner(db, log)
	h := hasher.NewHasher(ctx, db, log, sm)
	s := scorer.NewScorer(db, log, sm)
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

// SubmitScan creates a new scan session and kicks off the three-phase
// workflow in a background goroutine.
func (wf *Workflow) SubmitScan(paths []string) (uuid.UUID, error) {
	select {
	case <-wf.ctx.Done():
		return uuid.Nil, context.Canceled
	default:
	}

	cleanPaths, tracker, err := wf.prepareSession(wf.ctx, paths)
	if err != nil {
		return uuid.Nil, err
	}

	wf.activeSessions.Store(tracker.SessionID, tracker)

	wf.wg.Go(func() {
		wf.background(tracker, cleanPaths)
	})

	return tracker.SessionID, nil
}

// prepareSession creates the scan_sessions DB row, and returns a fresh tracker.
func (wf *Workflow) prepareSession(ctx context.Context, paths []string) ([]string, *sm.Tracker, error) {
	cleanPaths := make([]string, 0, len(paths))
	// Path convention:
	// 1) Resolve every input to a canonical absolute path.
	// 2) Store/log only the home-relative display form ("~/...") when under $HOME;
	//    keep absolute form otherwise.
	// This keeps DB rows portable across machines where the username/home prefix
	// can change while preserving stable locations outside $HOME.
	for _, inputPath := range paths {
		resolvedAbs, err := wf.path.RealPath(filepath.Clean(inputPath))
		if err != nil {
			wf.log.Error("invalid path", "path", inputPath, "error", err)
			return nil, nil, fmt.Errorf("path is not a directory: %s", inputPath)
		}

		cleanPaths = append(cleanPaths, wf.path.RelativeToHome(resolvedAbs))
	}

	wf.log.Info("Preparing scan session", "paths", cleanPaths)

	// Validate all paths before creating the session to fail fast on invalid input.
	for _, path := range cleanPaths {
		if ok, err := wf.path.IsDirectory(path); err != nil || !ok {
			wf.log.Error("invalid path, not a directory", "path", path, "error", err)
			return nil, nil, fmt.Errorf("path is not a directory: %s", path)
		}
	}

	wf.log.Info("All paths validated successfully", "paths", cleanPaths)

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
	`, sessionID, startedAt.Format(time.RFC3339), sm.WorkflowStatusStarted, strings.Join(cleanPaths, ","))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create scan session: %w", err)
	}

	wf.log.Info("Scan session created", "session_id", sessionID, "root_paths", cleanPaths)

	// Initialize the shared state tracker for this session
	progressCtx, cancelProgress := context.WithCancel(ctx)
	tracker := &sm.Tracker{
		SessionID: sessionID,
		Ctx:       progressCtx,
		Cancel:    cancelProgress,
	}
	tracker.Status.Store(sm.WorkflowStatusStarted)
	tracker.UnsupportedPaths.Store("")

	// Start the periodic progress updater for this session
	go wf.updateProgress(progressCtx, sessionID, tracker)

	return cleanPaths, tracker, nil
}

// background executes the three sequential phases for a single scan session.
func (wf *Workflow) background(tracker *sm.Tracker, paths []string) {
	var finalStatus string
	var finalErr *string
	workers := wf.workerCount(paths)

	defer func() {
		wf.finalizeSession(tracker.SessionID, finalStatus, finalErr)
	}()

	wf.log.Info("Workflow session started", "session_id", tracker.SessionID, "phases", "scan → hash → score")

	phases := wf.workflowPhases(tracker, paths, workers)

	for _, phase := range phases {
		count, status, errStr, ok := wf.run(tracker, phase.status, phase.run)
		finalStatus, finalErr = status, errStr
		if !ok {
			return
		}
		if phase.onSuccess != nil {
			phase.onSuccess(count)
		}
	}
}

func (wf *Workflow) workflowPhases(tracker *sm.Tracker, paths []string, workers int) []workflowPhase {
	return []workflowPhase{
		{
			status: sm.WorkflowStatusScan,
			run: func() (int, error) {
				return wf.scanner.Run(wf.ctx, tracker, paths, workers)
			},
			onSuccess: func(count int) {
				wf.writeUnsupportedFiles(tracker)
				wf.log.Info("Phase 1 complete: all paths scanned", "session_id", tracker.SessionID, "files_collected", count)
			},
		},
		{
			status: sm.WorkflowStatusHash,
			run: func() (int, error) {
				return wf.hasher.Run(wf.ctx, tracker, workers)
			},
			onSuccess: func(count int) {
				wf.log.Info("Phase 2 complete: all files hashed", "session_id", tracker.SessionID, "files_hashed", count)
			},
		},
		{
			status: sm.WorkflowStatusScore,
			run: func() (int, error) {
				return wf.scorer.Run(wf.ctx, tracker, paths, workers)
			},
			onSuccess: func(count int) {
				wf.log.Info("Phase 3 complete: all groups scored", "session_id", tracker.SessionID, "files_scored", count)
			},
		},
	}
}

// run runs a single workflow phase (Scan, Hash, or Score), handles logging,
// status updates, and consistent error reporting. Returns the result count,
// final status, error message (if any), and a boolean indicating success.
func (wf *Workflow) run(tracker *sm.Tracker, phaseStatus string, phaseFunc func() (int, error)) (int, string, *string, bool) {
	success := true
	tracker.Status.Store(phaseStatus)
	if err := wf.setSessionStatus(wf.ctx, tracker.SessionID, phaseStatus); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", phaseStatus, err).Error()
		return 0, sm.WorkflowStatusFail, &msg, !success
	}

	wf.log.Info("Starting phase", "session_id", tracker.SessionID, "phase", phaseStatus)
	count, err := phaseFunc()
	if err != nil {
		var finalStatus string
		var finalErr string
		if errors.Is(err, context.Canceled) {
			finalStatus = sm.WorkflowStatusCancelled
			finalErr = fmt.Sprintf("pipeline cancelled during %s phase", phaseStatus)
		} else {
			finalStatus = sm.WorkflowStatusFail
			finalErr = fmt.Sprintf("%s phase failed: %v", phaseStatus, err)
		}
		return count, finalStatus, &finalErr, !success
	}

	return count, phaseStatus, nil, success
}

func (wf *Workflow) workerCount(paths []string) int {
	return max(len(paths), wf.workers)
}
