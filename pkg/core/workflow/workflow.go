// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"github.com/jammutkarsh/wandersort/pkg/core/exif"
	"github.com/jammutkarsh/wandersort/pkg/core/hasher"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/core/scorer"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

// Workflow orchestrates the phases of one scan session. Scanning and hashing
// run in bounded batches to keep memory stable on very large roots.
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

	/* Pipeline components, run in this order */
	scanner *scanner.Scanner
	hasher  *hasher.Hasher
	exif    *exif.Extractor
	scorer  *scorer.Scorer
	vfs     *vfs.VFS

	// activeRoots holds the roots of in-flight sessions, so overlapping scans
	// are rejected before they re-stamp each other's rows. In-memory suffices:
	// the output-dir lock guarantees one scan/serve process.
	mu          sync.Mutex
	activeRoots map[uuid.UUID][]string

	wg sync.WaitGroup
}

type workflowPhase struct {
	kind workflowPhaseKind
	run  func() (int, error)
	// summary is the one user-facing line this phase reports on success. The
	// phase's elapsed time is appended to it rather than logged separately —
	// two console lines per phase ("Scanned 15481 files", "scan phase took
	// 1.996s") is twice the noise for one fact.
	summary func(count int) string
}

type workflowPhaseKind string

const (
	workflowPhaseScan  workflowPhaseKind = "scan"
	workflowPhaseHash  workflowPhaseKind = "hash"
	workflowPhaseExif  workflowPhaseKind = "exif"
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
	workflowPhaseExif:  {db.StatusAnalyzing, db.StatusAnalyzed, "Reading photo details…"},
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
func NewWorkflow(ctx context.Context, db *db.DB, locationResolver *location.Resolver, log logger.Logger, cfg *config.Configuration, exiftoolPath string) *Workflow {
	vfsCfg := vfs.ConfigFor(cfg)
	// all three come from flag/env/config.yaml, so showing the resolved values
	// is the only way to see which source won
	rules := "none (flat Year/Month)"
	if len(vfsCfg.Rules) > 0 {
		rules = strings.Join(vfsCfg.Rules, ", ")
	}
	log.Info("Pipeline configured", logger.UserKey, true,
		"workers", cfg.Workers,
		"output", filepath.Dir(cfg.AppDBPath),
		"rules", "Year/Month/"+rules)
	wf := &Workflow{
		ctx:              ctx,
		db:               db,
		locationResolver: locationResolver,
		scanner:          scanner.New(db, log, cfg.Workers),
		hasher:           hasher.New(db, log, cfg.Workers),
		exif:             exif.New(db, log, exiftoolPath, cfg.Workers),
		scorer:           scorer.New(db, log),
		vfs:              vfs.New(db, locationResolver, log, vfsCfg),
		log:              log,
		path:             path.New(),
		outputDir:        filepath.Dir(cfg.AppDBPath),
		activeRoots:      map[uuid.UUID][]string{},
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

// prepareSession creates the scan_sessions row and returns the new session ID.
// paths must already be canonical, deduplicated scan roots.
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

// claimRoots registers a session's roots after checking them against every
// in-flight session. Overlaps are rejected because the sweep reads "row not
// re-stamped by me" as proof a file vanished — two sessions over one tree
// would soft-delete each other's live rows.
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

// runSession runs the phases in order and finalizes the session. Returns the
// terminal status and error so RunScan can surface failure; SubmitScan ignores
// both.
func (wf *Workflow) runSession(sessionID uuid.UUID, paths []string) (finalStatus string, finalErr *string) {
	defer func() {
		wf.finalizeSession(sessionID, finalStatus, finalErr)
	}()

	wf.log.Info("Workflow session started", "sessionId", sessionID, "phases", "scanning → hashing → extracting → scoring → organizing")

	phases := wf.workflowPhases(sessionID, paths)

	for _, phase := range phases {
		_, status, errStr, ok := wf.run(sessionID, phase)
		finalStatus, finalErr = status, errStr
		if !ok {
			return
		}
	}

	// last thing before the session is marked done, so it sits next to the
	// "run wandersort review" hint rather than scrolling past mid-pipeline
	CheckOutputSpace(wf.ctx, wf.db, wf.log, wf.outputDir, sessionID)

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
			summary: func(count int) string { return fmt.Sprintf("Scanned %d files", count) },
		},
		{
			kind: workflowPhaseHash,
			run: func() (int, error) {
				return wf.hasher.Run(wf.ctx, sessionID)
			},
			summary: func(count int) string { return fmt.Sprintf("Checked %d files for duplicates", count) },
		},
		{
			kind: workflowPhaseExif,
			run: func() (int, error) {
				return wf.exif.Run(wf.ctx, sessionID)
			},
			summary: func(count int) string { return fmt.Sprintf("Read details from %d files", count) },
		},
		{
			kind: workflowPhaseScore,
			run: func() (int, error) {
				return wf.scorer.Run(wf.ctx, sessionID)
			},
			summary: func(count int) string { return fmt.Sprintf("Reviewed %d duplicate groups", count) },
		},
		{
			kind: workflowPhaseVFS,
			run: func() (int, error) {
				return wf.vfs.Run(wf.ctx, sessionID)
			},
			summary: func(count int) string { return fmt.Sprintf("Proposed destinations for %d files", count) },
		},
	}
}

// run executes one phase with its status writes and logging. Returns the
// result count, final status, error message, and whether it succeeded.
func (wf *Workflow) run(sessionID uuid.UUID, phase workflowPhase) (int, string, *string, bool) {
	success := true
	status := phase.kind.status()
	if err := wf.setSessionStatus(wf.ctx, sessionID, status.inProgress); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", status.inProgress, err).Error()
		return 0, db.StatusFailed, &msg, !success
	}

	wf.log.Info(status.message, logger.UserKey, true,
		logger.PhaseKey, string(phase.kind), logger.EventKey, "start", "sessionId", sessionID)
	start := time.Now()
	count, err := phase.run()
	elapsed := time.Since(start)
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

	wf.db.Writer.Flush() // make this phase's writes visible to the next one

	// one line per phase: what it did and how long it took
	msg := fmt.Sprintf("%s phase took %s", phase.kind, elapsed.Round(time.Millisecond))
	if phase.summary != nil {
		msg = fmt.Sprintf("%s in %s", phase.summary(count), elapsed.Round(time.Millisecond))
	}
	wf.log.Info(msg, logger.UserKey, true,
		logger.PhaseKey, string(phase.kind), logger.EventKey, "done",
		logger.ElapsedKey, elapsed.Round(time.Millisecond).String(), "sessionId", sessionID)

	if err := wf.setSessionStatus(wf.ctx, sessionID, status.completed); err != nil {
		msg := fmt.Errorf("failed to set %s status: %w", status.completed, err).Error()
		return count, db.StatusFailed, &msg, !success
	}

	return count, status.completed, nil, success
}
