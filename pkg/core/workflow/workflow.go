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

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/metadata"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/core/scorer"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// Deps supplies the two downloadable dependencies, blocking until each
// exists — only the metadata and vfs phases call these, so the walk can start
// while the downloads are still running.
type Deps struct {
	Exiftool func() (string, error)             // path to the exiftool binary
	Location func() (*location.Resolver, error) // open geonames resolver
}

// Workflow orchestrates the phases of one scan session. Scanning and metadata
// extraction run in bounded batches to keep memory stable on very large roots.
type Workflow struct {
	ctx     context.Context
	db      *db.DB
	log     logger.Logger
	deps    Deps
	workers int

	// appCfg is swappable while the pipeline runs (UpdateConfig): the shell
	// hosts the settings wizard and the scan in one program, so a save can
	// land mid-run. Only the vfs phase re-reads it — everything above it has
	// already used what it needed.
	mu      sync.Mutex
	appCfg  *config.Configuration
	cfgDirt bool

	/* Utilities */
	path      *path.Resolver
	outputDir string

	scanner *scanner.Scanner
	scorer  *scorer.Scorer
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
	workflowPhaseScan workflowPhaseKind = "scan"
	// hashing and EXIF are one phase: reading each file twice cost a second
	// trip to disk once the page cache had evicted it (see pkg/core/metadata)
	workflowPhaseMetadata workflowPhaseKind = "metadata"
	workflowPhaseScore    workflowPhaseKind = "score"
	workflowPhaseVFS      workflowPhaseKind = "vfs"
)

// phaseMessageByKind is the one user-facing line logged when a phase starts.
var phaseMessageByKind = map[workflowPhaseKind]string{
	workflowPhaseScan:     "Scanning your files…",
	workflowPhaseMetadata: "Reading your files…",
	workflowPhaseScore:    "Selecting the best copy of each duplicate…",
	workflowPhaseVFS:      "Proposing an organized folder structure…",
}

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
		appCfg:    cfg,
		scanner:   scanner.New(db, log, cfg.Workers),
		scorer:    scorer.New(db, log),
		workers:   cfg.Workers,
		log:       log,
		path:      path.New(),
		outputDir: filepath.Dir(cfg.AppDBPath),
	}
	return wf
}

// UpdateConfig swaps the settings the vfs phase builds its proposal from, so
// a wizard save inside the shell retargets the run that is already going.
// Safe from any goroutine; takes effect at the next vfs (re)start. The phases
// above vfs read nothing from it, so nothing else needs to be told.
func (wf *Workflow) UpdateConfig(cfg *config.Configuration) {
	wf.mu.Lock()
	defer wf.mu.Unlock()
	wf.appCfg = cfg
	wf.cfgDirt = true
}

// takeConfig reads the settings for one vfs pass and marks them as used, so a
// save that arrives during the pass is what configChanged then reports.
func (wf *Workflow) takeConfig() *config.Configuration {
	wf.mu.Lock()
	defer wf.mu.Unlock()
	wf.cfgDirt = false
	return wf.appCfg
}

// configChanged reports a save that landed since the last takeConfig.
func (wf *Workflow) configChanged() bool {
	wf.mu.Lock()
	defer wf.mu.Unlock()
	return wf.cfgDirt
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

	wf.log.Info("Workflow started", "phases", "scanning → extracting → scoring → organizing")

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
	volume.CheckOutputSpace(wf.ctx, wf.db, wf.log, wf.outputDir)

	finalStatus = db.StatusCompleted
	return
}

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
			kind: workflowPhaseMetadata,
			run: func() (int, error) {
				// blocks here (not at construction) if exiftool is still
				// downloading — the walk has already run meanwhile
				exiftoolPath, err := wf.deps.Exiftool()
				if err != nil {
					return 0, fmt.Errorf("exiftool: %w", err)
				}
				return metadata.New(wf.db, wf.log, exiftoolPath, wf.workers).Run(wf.ctx)
			},
			summary: func(count int) string { return fmt.Sprintf("Read %d files", count) },
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
				resolver, err := wf.deps.Location()
				if err != nil {
					return 0, fmt.Errorf("location resolver: %w", err)
				}
				// A settings save that lands while this is running gets a
				// second pass rather than a cancelled first one: vfs.Propose
				// replaces the proposal wholesale and is idempotent, so
				// letting it finish and re-running costs one pass and needs no
				// context surgery or half-written state.
				for {
					count, err := vfs.Propose(wf.ctx, wf.db, resolver, wf.takeConfig(), wf.log)
					if err != nil || !wf.configChanged() {
						return count, err
					}
					wf.log.Info("Settings changed — rebuilding folder proposal with new settings",
						logger.UserKey, true)
				}
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

	// Checkpoint after every phase, not just at the end: a small WAL keeps the
	// next phase's reads/writes cheaper. Not fatal — a failed one just leaves
	// more for the next checkpoint (or Close) to do.
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

// finalizeSession logs the pipeline's terminal outcome
func (wf *Workflow) finalizeSession(finalStatus string, finalErr *string) {
	if finalErr != nil {
		wf.log.Error("Pipeline finished", "status", finalStatus, "error", *finalErr)
		return
	}
	wf.log.Info("Pipeline finished", "status", finalStatus)
}
