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
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

// Workflow orchestrates scan, hash and score workflow for each session
// Scanning and hashing run in bounded batches to keep memory usage stable on
// very large roots
type Workflow struct {
	ctx              context.Context
	db               *db.DB
	locationResolver *location.Resolver
	log              logger.Logger

	/* Utilities */
	path *path.Resolver

	/* Pipeline components */
	scanner *scanner.Scanner
	hasher  *hasher.Hasher
	scorer  *scorer.Scorer

	wg sync.WaitGroup
}

type workflowPhase struct {
	kind      workflowPhaseKind
	run       func() (int, error)
	onSuccess func(count int)
}

type workflowPhaseKind string

const (
	workflowPhaseScan  workflowPhaseKind = "scan"
	workflowPhaseHash  workflowPhaseKind = "hash"
	workflowPhaseScore workflowPhaseKind = "score"
	// defaultFinalizeTimeout is the deadline for writing the final session
	// status when the pipeline completed without interruption
	defaultFinalizeTimeout = 15 * time.Second
)

func (kind workflowPhaseKind) inProgressStatus() string {
	switch kind {
	case workflowPhaseScan:
		return db.StatusScanning
	case workflowPhaseHash:
		return db.StatusHashing
	case workflowPhaseScore:
		return db.StatusScoring
	default:
		return db.StatusFailed
	}
}

func (kind workflowPhaseKind) completedStatus() string {
	switch kind {
	case workflowPhaseScan:
		return db.StatusScanned
	case workflowPhaseHash:
		return db.StatusHashed
	case workflowPhaseScore:
		return db.StatusScored
	default:
		return db.StatusFailed
	}
}

// NewWorkflow creates a new workflow instance
func NewWorkflow(ctx context.Context, db *db.DB, locationResolver *location.Resolver, log logger.Logger, cfg *config.Configuration, exiftoolPath string) *Workflow {
	return &Workflow{
		ctx:              ctx,
		db:               db,
		locationResolver: locationResolver,
		scanner:          scanner.New(db, log, cfg.Workers),
		hasher:           hasher.New(ctx, db, log, exiftoolPath, cfg.Workers),
		scorer:           scorer.New(db, log),
		log:              log,
		path:             path.New(),
	}
}

// SubmitScan creates a new scan session and kicks off the workflow
// workflow in a background goroutine
func (wf *Workflow) SubmitScan(paths []string) (uuid.UUID, error) {
	select {
	case <-wf.ctx.Done():
		return uuid.Nil, context.Canceled
	default:
	}

	sessionID, err := wf.prepareSession(wf.ctx, paths)
	if err != nil {
		return uuid.Nil, err
	}

	wf.wg.Go(func() {
		wf.background(sessionID, paths)
	})

	return sessionID, nil
}

// prepareSession creates the scan_sessions DB row and returns the new session ID
//
// The incoming paths are expected to already be canonical, validated scan roots
// API-level preparation resolves, deduplicates, and prunes overlapping paths
// before this method runs
func (wf *Workflow) prepareSession(ctx context.Context, paths []string) (uuid.UUID, error) {
	storedPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		storedPaths = append(storedPaths, wf.path.RelativeToHome(path))
	}

	wf.log.Info("Preparing scan session", "paths", storedPaths)

	// Create scan session
	sessionID, _ := uuid.NewV7()
	if sessionID == uuid.Nil {
		sessionID = uuid.New()
	}
	startedAt := time.Now().UTC()
	_, err := wf.db.ExecContext(ctx, `
		INSERT INTO scan_sessions (id, started_at, status, root_paths)
		VALUES (?, ?, ?, ?)
	`, sessionID, startedAt.Format(time.RFC3339), db.StatusStarted, strings.Join(storedPaths, ","))
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to create scan session: %w", err)
	}

	wf.log.Info("Scan session created", "sessionId", sessionID, "rootPaths", storedPaths)

	return sessionID, nil
}

// background executes the sequential phases for a single scan session
func (wf *Workflow) background(sessionID uuid.UUID, paths []string) {
	var finalStatus string
	var finalErr *string

	defer func() {
		wf.finalizeSession(sessionID, finalStatus, finalErr)
	}()

	wf.log.Info("Workflow session started", "sessionId", sessionID, "phases", "scanning → hashing → scoring")

	phases := wf.workflowPhases(sessionID, paths)

	for _, phase := range phases {
		count, status, errStr, ok := wf.run(sessionID, phase.kind, phase.run)
		finalStatus, finalErr = status, errStr
		if !ok {
			return
		}
		if phase.onSuccess != nil {
			phase.onSuccess(count)
		}
	}

	if err := wf.setSessionStatus(wf.ctx, sessionID, db.StatusCompleted); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", db.StatusCompleted, err).Error()
		finalStatus = db.StatusFailed
		finalErr = &msg
		return
	}
	finalStatus = db.StatusCompleted
}

// workflowPhases builds the ordered list of pipeline phases for a session
func (wf *Workflow) workflowPhases(sessionID uuid.UUID, paths []string) []workflowPhase {
	return []workflowPhase{
		{
			kind: workflowPhaseScan,
			run: func() (int, error) {
				return wf.scanner.Run(wf.ctx, sessionID, paths)
			},
			onSuccess: func(count int) {
				wf.log.Info("Phase 1 complete: all paths scanned", "sessionId", sessionID, "filesCollected", count)
			},
		},
		/*
			TODO(#22): the hasher currently uses BLAKE3 on the full file bytes, so two
			pixel-identical photos with different embedded metadata (EXIF, ICC profile)
			produce different hashes and land in separate content groups.  This means the
			scorer cannot elect between them — each becomes a solo master and both
			survive.  Until pixel-level and perceptual hashing layers are added, the
			only signal available for picking the best copy is the folder-naming heuristics in the scorer.
		*/
		{
			kind: workflowPhaseHash,
			run: func() (int, error) {
				return wf.hasher.Run(wf.ctx, sessionID)
			},
			onSuccess: func(count int) {
				wf.log.Info("Phase 2 complete: all files hashed", "sessionId", sessionID, "filesHashed", count)
			},
		},
		{
			kind: workflowPhaseScore,
			run: func() (int, error) {
				return wf.scorer.Run(wf.ctx, sessionID)
			},
			onSuccess: func(count int) {
				wf.log.Info("Phase 3 complete: all groups scored", "sessionId", sessionID, "groupsScored", count)
			},
		},
	}
}

// run runs a single workflow phase, handles logging,
// status updates, and consistent error reporting. Returns the result count,
// final status, error message (if any), and a boolean indicating success
func (wf *Workflow) run(sessionID uuid.UUID, phase workflowPhaseKind, phaseFunc func() (int, error)) (int, string, *string, bool) {
	success := true
	inProgressStatus := phase.inProgressStatus()
	if err := wf.setSessionStatus(wf.ctx, sessionID, inProgressStatus); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", inProgressStatus, err).Error()
		return 0, db.StatusFailed, &msg, !success
	}

	wf.log.Info("Starting phase", "sessionId", sessionID, "phase", inProgressStatus)
	count, err := phaseFunc()
	if err != nil {
		var finalStatus string
		var finalErr string
		if errors.Is(err, context.Canceled) {
			finalStatus = db.StatusCancelled
			finalErr = fmt.Sprintf("pipeline cancelled during %s phase", inProgressStatus)
		} else {
			finalStatus = db.StatusFailed
			finalErr = fmt.Sprintf("%s phase failed: %v", inProgressStatus, err)
		}
		return count, finalStatus, &finalErr, !success
	}

	wf.db.Writer.Flush()

	completedStatus := phase.completedStatus()
	if err := wf.setSessionStatus(wf.ctx, sessionID, completedStatus); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", completedStatus, err).Error()
		return count, db.StatusFailed, &msg, !success
	}

	return count, completedStatus, nil, success
}
