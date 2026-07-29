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
	"time"

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

// Deps supplies the two downloadable dependencies, blocking until each exists.
// This is what lets a first-ever scan start walking files while the downloads
// are still running: scan/hash need nothing, only the exif phase calls
// Exiftool() and only the vfs phase calls Location(), so each phase stalls
// exactly as long as its own dependency — usually not at all.
type Deps struct {
	Exiftool func() (string, error)             // path to the exiftool binary
	Location func() (*location.Resolver, error) // open gazetteer resolver
}

// Workflow orchestrates the phases of one scan session. Scanning and hashing
// run in bounded batches to keep memory stable on very large roots.
type Workflow struct {
	ctx  context.Context
	db   *db.DB
	log  logger.Logger
	deps Deps

	/* Utilities */
	path *path.Resolver
	// outputDir is where the organized library (and the DB) lives; used for
	// the free-space preflight after each scan
	outputDir string

	/* Pipeline components, run in this order. exif and vfs are built lazily in
	their phase closures — their dependencies may still be downloading when the
	workflow is constructed (see Deps). */
	scanner *scanner.Scanner
	hasher  *hasher.Hasher
	scorer  *scorer.Scorer
	workers int
	vfsCfg  vfs.Config
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
)

// phaseMessageByKind is the one user-facing line logged when a phase starts.
var phaseMessageByKind = map[workflowPhaseKind]string{
	workflowPhaseScan:  "Scanning your files…",
	workflowPhaseHash:  "Looking for duplicate files…",
	workflowPhaseExif:  "Reading photo details…",
	workflowPhaseScore: "Selecting the best copy of each duplicate…",
	workflowPhaseVFS:   "Proposing an organized folder structure…",
}

// NewWorkflow creates a new workflow instance
func NewWorkflow(ctx context.Context, db *db.DB, log logger.Logger, cfg *config.Configuration, deps Deps) *Workflow {
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
		ctx:       ctx,
		db:        db,
		deps:      deps,
		scanner:   scanner.New(db, log, cfg.Workers),
		hasher:    hasher.New(db, log, cfg.Workers),
		scorer:    scorer.New(db, log),
		workers:   cfg.Workers,
		vfsCfg:    vfsCfg,
		log:       log,
		path:      path.New(),
		outputDir: filepath.Dir(cfg.AppDBPath),
	}
	return wf
}

// RunScan canonicalizes and prunes nested scan roots, then runs the pipeline
// synchronously on the calling goroutine, so a CLI invocation streams progress
// and blocks until the scan finishes. Returns the roots actually walked, and
// an error if the run did not complete.
func (wf *Workflow) RunScan(paths []string) ([]string, error) {
	select {
	case <-wf.ctx.Done():
		return nil, context.Canceled
	default:
	}

	roots, err := path.ReduceRoots(wf.path, paths)
	if err != nil {
		wf.log.Warn("Invalid scan roots", "error", err)
		return nil, err
	}

	storedPaths := make([]string, 0, len(roots))
	for _, p := range roots {
		storedPaths = append(storedPaths, wf.path.RelativeToHome(p))
	}
	wf.log.Info("Starting scan", logger.UserKey, true, "paths", storedPaths)

	status, errStr := wf.runSession(roots)
	if status != db.StatusCompleted {
		if errStr != nil {
			return roots, errors.New(*errStr)
		}
		return roots, fmt.Errorf("scan ended with status %s", status)
	}

	return roots, nil
}

// runSession runs the phases in order and finalizes the run. Returns the
// terminal status and error so RunScan can surface failure.
func (wf *Workflow) runSession(paths []string) (finalStatus string, finalErr *string) {
	defer func() {
		wf.finalizeSession(finalStatus, finalErr)
	}()

	wf.log.Info("Workflow started", "phases", "scanning → hashing → extracting → scoring → organizing")

	phases := wf.workflowPhases(paths)

	for _, phase := range phases {
		_, status, errStr, ok := wf.run(phase)
		finalStatus, finalErr = status, errStr
		if !ok {
			return
		}
	}

	// last thing before the run is marked done, so it sits next to the
	// "run wandersort review" hint rather than scrolling past mid-pipeline
	CheckOutputSpace(wf.ctx, wf.db, wf.log, wf.outputDir)

	finalStatus = db.StatusCompleted
	return
}

// workflowPhases builds the ordered list of pipeline phases for a run
func (wf *Workflow) workflowPhases(paths []string) []workflowPhase {
	return []workflowPhase{
		{
			kind: workflowPhaseScan,
			run: func() (int, error) {
				return wf.scanner.Run(wf.ctx, paths)
			},
			summary: func(count int) string { return fmt.Sprintf("Scanned %d files", count) },
		},
		{
			kind: workflowPhaseHash,
			run: func() (int, error) {
				return wf.hasher.Run(wf.ctx)
			},
			summary: func(count int) string { return fmt.Sprintf("Checked %d files for duplicates", count) },
		},
		{
			kind: workflowPhaseExif,
			run: func() (int, error) {
				// blocks here (not at construction) if exiftool is still
				// downloading — scan and hash have already run meanwhile
				exiftoolPath, err := wf.deps.Exiftool()
				if err != nil {
					return 0, fmt.Errorf("exiftool: %w", err)
				}
				return exif.New(wf.db, wf.log, exiftoolPath, wf.workers).Run(wf.ctx)
			},
			summary: func(count int) string { return fmt.Sprintf("Read details from %d files", count) },
		},
		{
			kind: workflowPhaseScore,
			run: func() (int, error) {
				return wf.scorer.Run(wf.ctx)
			},
			summary: func(count int) string { return fmt.Sprintf("Reviewed %d duplicate groups", count) },
		},
		{
			kind: workflowPhaseVFS,
			run: func() (int, error) {
				// the location DB is the big download; being the last phase gives
				// it the whole pipeline to finish behind
				resolver, err := wf.deps.Location()
				if err != nil {
					return 0, fmt.Errorf("location resolver: %w", err)
				}
				return vfs.New(wf.db, resolver, wf.log, wf.vfsCfg).Run(wf.ctx)
			},
			summary: func(count int) string { return fmt.Sprintf("Proposed destinations for %d files", count) },
		},
	}
}

// run executes one phase and logs its start/end. Returns the result count,
// final status, error message, and whether it succeeded.
func (wf *Workflow) run(phase workflowPhase) (int, string, *string, bool) {
	success := true
	message := phaseMessageByKind[phase.kind]
	if message == "" {
		message = "Working…"
	}

	wf.log.Info(message, logger.UserKey, true,
		logger.PhaseKey, string(phase.kind), logger.EventKey, "start")
	start := time.Now()
	count, err := phase.run()
	elapsed := time.Since(start)
	if err != nil {
		var finalStatus string
		var finalErr string
		if errors.Is(err, context.Canceled) {
			finalStatus = db.StatusCancelled
			finalErr = fmt.Sprintf("pipeline cancelled during %s phase", phase.kind)
		} else {
			finalStatus = db.StatusFailed
			finalErr = fmt.Sprintf("%s phase failed: %v", phase.kind, err)
		}
		return count, finalStatus, &finalErr, !success
	}

	wf.db.Writer.Flush() // make this phase's writes visible to the next one

	// Checkpoint after every phase, not just at the end: each phase writes a
	// batch (file_registry, file_metadata, virtual_fs_entries, ...), and a
	// small WAL keeps the next phase's own reads/writes cheaper than letting
	// it grow across the whole run. Not fatal — a failed checkpoint just
	// means the next one (or Close, on a run that ends early) has more to do.
	if cpErr := wf.db.Checkpoint(); cpErr != nil {
		wf.log.Warn("Database checkpoint failed", "phase", string(phase.kind), "error", cpErr)
	}

	// one line per phase: what it did and how long it took
	msg := fmt.Sprintf("%s phase took %s", phase.kind, elapsed.Round(time.Millisecond))
	if phase.summary != nil {
		msg = fmt.Sprintf("%s in %s", phase.summary(count), elapsed.Round(time.Millisecond))
	}
	wf.log.Info(msg, logger.UserKey, true,
		logger.PhaseKey, string(phase.kind), logger.EventKey, "done",
		logger.ElapsedKey, elapsed.Round(time.Millisecond).String())

	return count, "", nil, success
}
