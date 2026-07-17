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
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
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
	// outputDir is where the organized library (and the DB) lives; used for
	// the free-space preflight after each scan
	outputDir string

	/* Pipeline components */
	scanner Scanner
	hasher  Hasher
	scorer  Scorer
	vfs     VFS

	// activeRoots tracks the scan roots of in-flight sessions so overlapping
	// scans are rejected before they can re-stamp each other's registry rows.
	// In-memory is sufficient: the output-dir lock guarantees a single
	// scan/serve process, so every live session lives in this Workflow
	mu          sync.Mutex
	activeRoots map[uuid.UUID][]string

	wg sync.WaitGroup
}

// Scanner, Hasher and Scorer are the three pipeline phases. Interfaces so
// tests and alternate strategies (see TODO #22 on hashing) can substitute
// implementations without touching the orchestrator.
type (
	Scanner interface {
		Run(ctx context.Context, sessionID uuid.UUID, paths []string) (int, error)
	}
	Hasher interface {
		Run(ctx context.Context, sessionID uuid.UUID) (int, error)
	}
	Scorer interface {
		Run(ctx context.Context, sessionID uuid.UUID) (int, error)
	}
	VFS interface {
		Run(ctx context.Context, sessionID uuid.UUID) (int, error)
	}
)

// Option overrides a default pipeline component on NewWorkflow.
type Option func(*Workflow)

func WithScanner(s Scanner) Option { return func(wf *Workflow) { wf.scanner = s } }

func WithHasher(h Hasher) Option { return func(wf *Workflow) { wf.hasher = h } }

func WithScorer(s Scorer) Option { return func(wf *Workflow) { wf.scorer = s } }

func WithVFS(v VFS) Option { return func(wf *Workflow) { wf.vfs = v } }

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
	workflowPhaseVFS   workflowPhaseKind = "vfs"
	// defaultFinalizeTimeout is the deadline for writing the final session
	// status when the pipeline completed without interruption
	defaultFinalizeTimeout = 15 * time.Second
)

// phaseStatus holds the status transitions and console message for a workflow phase.
type phaseStatus struct {
	inProgress string
	completed  string
	message    string
}

var phaseStatusByKind = map[workflowPhaseKind]phaseStatus{
	workflowPhaseScan:  {db.StatusScanning, db.StatusScanned, "Scanning your files…"},
	workflowPhaseHash:  {db.StatusHashing, db.StatusHashed, "Looking for duplicate files…"},
	workflowPhaseScore: {db.StatusScoring, db.StatusScored, "Selecting the best copy of each duplicate…"},
	workflowPhaseVFS:   {db.StatusOrganizing, db.StatusOrganized, "Proposing an organized folder structure…"},
}

func (kind workflowPhaseKind) status() phaseStatus {
	if s, ok := phaseStatusByKind[kind]; ok {
		return s
	}
	return phaseStatus{db.StatusFailed, db.StatusFailed, "Working…"}
}

// NewWorkflow creates a new workflow instance
func NewWorkflow(ctx context.Context, db *db.DB, locationResolver *location.Resolver, log logger.Logger, cfg *config.Configuration, exiftoolPath string, opts ...Option) *Workflow {
	log.Info("Pipeline configured", "workers", cfg.Workers)
	wf := &Workflow{
		ctx:              ctx,
		db:               db,
		locationResolver: locationResolver,
		scanner:          scanner.New(db, log, cfg.Workers),
		hasher:           hasher.New(ctx, db, log, exiftoolPath, cfg.Workers),
		scorer:           scorer.New(db, log),
		vfs:              vfs.New(db, locationResolver, log, vfs.DefaultConfig()),
		log:              log,
		path:             path.New(),
		outputDir:        filepath.Dir(cfg.AppDBPath),
		activeRoots:      map[uuid.UUID][]string{},
	}
	for _, opt := range opts {
		opt(wf)
	}
	return wf
}

// SubmitScan creates a new scan session and runs the pipeline in a background
// goroutine. Used by the HTTP server, which returns the session ID immediately
// and reports progress through the sessionId-keyed log stream. CLI scans want
// foreground progress instead — use RunScan.
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
		wf.runSession(sessionID, paths)
	})

	return sessionID, nil
}

// RunScan creates a scan session and runs the pipeline synchronously on the
// calling goroutine, so a CLI invocation streams progress and blocks until the
// scan finishes. Returns an error if the session did not complete.
func (wf *Workflow) RunScan(paths []string) (uuid.UUID, error) {
	select {
	case <-wf.ctx.Done():
		return uuid.Nil, context.Canceled
	default:
	}

	sessionID, err := wf.prepareSession(wf.ctx, paths)
	if err != nil {
		return uuid.Nil, err
	}

	status, errStr := wf.runSession(sessionID, paths)
	if status != db.StatusCompleted {
		if errStr != nil {
			return sessionID, errors.New(*errStr)
		}
		return sessionID, fmt.Errorf("scan ended with status %s", status)
	}

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

	if err := wf.claimRoots(sessionID, paths); err != nil {
		return uuid.Nil, err
	}

	_, err := wf.db.ExecContext(ctx, `
		INSERT INTO scan_sessions (id, started_at, status, root_paths)
		VALUES (?, ?, ?, ?)
	`, sessionID, db.FormatTime(time.Now()), db.StatusStarted, strings.Join(storedPaths, ","))
	if err != nil {
		wf.releaseRoots(sessionID)
		return uuid.Nil, fmt.Errorf("failed to create scan session: %w", err)
	}
	msg := fmt.Sprintf("Started session %s", sessionID)
	wf.log.Info(msg, logger.UserKey, true, "sessionId", sessionID, "rootPaths", storedPaths)

	return sessionID, nil
}

// claimRoots atomically checks the new session's roots against every
// in-flight session and registers them. Overlapping concurrent scans are
// rejected: the sweep treats "row not re-stamped by me" as proof a file
// vanished, so two sessions over the same tree would soft-delete each
// other's live rows
func (wf *Workflow) claimRoots(sessionID uuid.UUID, paths []string) error {
	wf.mu.Lock()
	defer wf.mu.Unlock()
	for activeID, activePaths := range wf.activeRoots {
		for _, active := range activePaths {
			for _, p := range paths {
				if path.Overlaps(active, p) {
					return fmt.Errorf("path %s overlaps %s, which session %s is still scanning", p, active, activeID)
				}
			}
		}
	}
	wf.activeRoots[sessionID] = paths
	return nil
}

// releaseRoots frees a session's claimed scan roots
func (wf *Workflow) releaseRoots(sessionID uuid.UUID) {
	wf.mu.Lock()
	defer wf.mu.Unlock()
	delete(wf.activeRoots, sessionID)
}

// runSession executes the sequential phases for a single scan session and
// finalizes it. Returns the terminal status and error message (if any) so a
// synchronous caller can surface failure; the async caller ignores them.
func (wf *Workflow) runSession(sessionID uuid.UUID, paths []string) (finalStatus string, finalErr *string) {
	defer func() {
		wf.finalizeSession(sessionID, finalStatus, finalErr)
	}()

	wf.log.Info("Workflow session started", "sessionId", sessionID, "phases", "scanning → hashing → scoring → organizing")

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
	return
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
				msg := fmt.Sprintf("Scanned %d files", count)
				wf.log.Info(msg, logger.UserKey, true, "sessionId", sessionID)
				wf.warnIfLowSpace(sessionID)
			},
		},
		{
			kind: workflowPhaseHash,
			run: func() (int, error) {
				return wf.hasher.Run(wf.ctx, sessionID)
			},
			onSuccess: func(count int) {
				msg := fmt.Sprintf("Checked %d files for duplicates", count)
				wf.log.Info(msg, logger.UserKey, true, "sessionId", sessionID)
			},
		},
		{
			kind: workflowPhaseScore,
			run: func() (int, error) {
				return wf.scorer.Run(wf.ctx, sessionID)
			},
			onSuccess: func(count int) {
				msg := fmt.Sprintf("Reviewed %d duplicate groups", count)
				wf.log.Info(msg, logger.UserKey, true, "sessionId", sessionID)
			},
		},
		{
			kind: workflowPhaseVFS,
			run: func() (int, error) {
				return wf.vfs.Run(wf.ctx, sessionID)
			},
			onSuccess: func(count int) {
				msg := fmt.Sprintf("Proposed destinations for %d files", count)
				wf.log.Info(msg, logger.UserKey, true, "sessionId", sessionID)
			},
		},
	}
}

// run runs a single workflow phase, handles logging,
// status updates, and consistent error reporting. Returns the result count,
// final status, error message (if any), and a boolean indicating success
func (wf *Workflow) run(sessionID uuid.UUID, phase workflowPhaseKind, phaseFunc func() (int, error)) (int, string, *string, bool) {
	success := true
	status := phase.status()
	if err := wf.setSessionStatus(wf.ctx, sessionID, status.inProgress); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", status.inProgress, err).Error()
		return 0, db.StatusFailed, &msg, !success
	}

	wf.log.Info(status.message, logger.UserKey, true, "sessionId", sessionID)
	count, err := phaseFunc()
	if err != nil {
		var finalStatus string
		var finalErr string
		if errors.Is(err, context.Canceled) {
			finalStatus = db.StatusCancelled
			finalErr = fmt.Sprintf("pipeline cancelled during %s phase", status.inProgress)
		} else {
			finalStatus = db.StatusFailed
			finalErr = fmt.Sprintf("%s phase failed: %v", status.inProgress, err)
		}
		return count, finalStatus, &finalErr, !success
	}

	wf.db.Writer.Flush()

	if err := wf.setSessionStatus(wf.ctx, sessionID, status.completed); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", status.completed, err).Error()
		return count, db.StatusFailed, &msg, !success
	}

	return count, status.completed, nil, success
}
