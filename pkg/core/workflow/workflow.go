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
	"github.com/jammutkarsh/wandersort/pkg/core/metadata"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
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

// ErrOverlapsLibrary means a folder to scan is the library, or holds it, or
// sits inside it. Scanning the library as a source would re-read files it
// already holds and plan any file no row records into the library again.
var ErrOverlapsLibrary = errors.New("overlaps the library")

// Workflow orchestrates the phases of one scan.
type Workflow struct {
	db      *db.DB
	log     logger.Logger
	deps    Deps
	workers int
	appCfg  *config.Configuration

	/* Utilities */
	path      *path.Resolver
	outputDir string

	scanner *scanner.Scanner
}

type workflowPhase struct {
	kind workflowPhaseKind
	run  func(ctx context.Context) (int, error)
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
	// electing one copy of each duplicate is not a phase of its own: it is a
	// pure function of the rows the vfs phase already loads (see vfs/elect.go)
	workflowPhaseVFS workflowPhaseKind = "vfs"
)

// phaseMessageByKind is the one user-facing line logged when a phase starts.
var phaseMessageByKind = map[workflowPhaseKind]string{
	workflowPhaseScan:     "Scanning your files…",
	workflowPhaseMetadata: "Reading your files…",
	workflowPhaseVFS:      "Proposing an organized folder structure…",
}

func NewWorkflow(db *db.DB, log logger.Logger, cfg *config.Configuration, deps Deps) *Workflow {
	vfsCfg := vfs.ConfigFor(cfg)
	// the output folder comes from --output-path or the library history and
	// the rules from the library's own settings, so showing them up front is
	// the only way to see what this run will do
	rules := "none (flat Year/Month)"
	if len(vfsCfg.Rules) > 0 {
		rules = strings.Join(vfsCfg.Rules, ", ")
	}
	log.Info("Pipeline configured", logger.UserKey, true,
		"workers", cfg.Workers,
		"output", filepath.Dir(cfg.AppDBPath),
		"rules", "Year/Month/"+rules)
	return &Workflow{
		db:        db,
		deps:      deps,
		appCfg:    cfg,
		scanner:   scanner.New(db, log, cfg.Workers),
		workers:   cfg.Workers,
		log:       log,
		path:      path.New(),
		outputDir: filepath.Dir(cfg.AppDBPath),
	}
}

// RunScan canonicalizes and prunes nested scan roots, then runs the pipeline
// synchronously on the calling goroutine, so a CLI invocation streams progress
// and blocks until the scan finishes. Returns the roots actually walked, and
// an error if the run did not complete — wrapping context.Canceled when it was
// stopped, so callers can tell the two apart with errors.Is. force re-reads
// every file from disk (re-hash + re-exiftool) even when its size/mtime
// haven't changed — for picking up a change to WanderSort's own extraction
// logic without deleting the database.
func (wf *Workflow) RunScan(ctx context.Context, paths []string, force bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	roots, err := path.ReduceRoots(wf.path, paths)
	if err != nil {
		wf.log.Warn("Invalid scan roots", "error", err)
		return nil, err
	}
	if err := wf.checkOverlap(roots); err != nil {
		return nil, err
	}

	storedPaths := make([]string, 0, len(roots))
	for _, p := range roots {
		storedPaths = append(storedPaths, wf.path.RelativeToHome(p))
	}
	wf.log.Info("Starting scan", logger.UserKey, true, "paths", storedPaths)

	if err := wf.runPhases(ctx, roots, force); err != nil {
		wf.log.Error("Pipeline finished", "error", err)
		return roots, err
	}
	wf.log.Info("Pipeline finished")
	return roots, nil
}

// checkOverlap refuses any root that is the library, holds it, or sits in it.
// The library folder always exists by now (the database was opened in it).
func (wf *Workflow) checkOverlap(roots []string) error {
	library, err := wf.path.RealPath(wf.outputDir)
	if err != nil {
		return fmt.Errorf("resolve library folder: %w", err)
	}
	for _, root := range roots {
		if path.Overlaps(root, library) {
			return fmt.Errorf("cannot scan %s: it %w at %s — pick folders outside it",
				wf.path.RelativeToHome(root), ErrOverlapsLibrary, wf.path.RelativeToHome(library))
		}
	}
	return nil
}

// runPhases runs the phases in order and stops at the first that fails.
func (wf *Workflow) runPhases(ctx context.Context, paths []string, force bool) error {
	wf.log.Info("Workflow started", "phases", "scanning → reading → organizing")
	for _, phase := range wf.workflowPhases(paths, force) {
		if _, err := wf.run(ctx, phase); err != nil {
			return err
		}
	}
	// last thing before the run is done, so it sits next to the "review"
	// hint rather than scrolling past mid-pipeline
	volume.CheckOutputSpace(ctx, wf.db, wf.log, wf.outputDir)
	return nil
}

func (wf *Workflow) workflowPhases(paths []string, force bool) []workflowPhase {
	return []workflowPhase{
		{
			kind: workflowPhaseScan,
			run: func(ctx context.Context) (int, error) {
				return wf.scanner.Run(ctx, paths, force)
			},
			summary: func(count int) string { return fmt.Sprintf("Scanned %d files", count) },
		},
		{
			kind: workflowPhaseMetadata,
			run: func(ctx context.Context) (int, error) {
				// blocks here (not at construction) if exiftool is still
				// downloading — the walk has already run meanwhile
				exiftoolPath, err := wf.deps.Exiftool()
				if err != nil {
					return 0, fmt.Errorf("exiftool: %w", err)
				}
				return metadata.New(wf.db, wf.log, exiftoolPath, wf.workers).Run(ctx)
			},
			summary: func(count int) string { return fmt.Sprintf("Read %d files", count) },
		},
		{
			kind: workflowPhaseVFS,
			run: func(ctx context.Context) (int, error) {
				resolver, err := wf.deps.Location()
				if err != nil {
					return 0, fmt.Errorf("location resolver: %w", err)
				}
				return vfs.Propose(ctx, wf.db, resolver, wf.appCfg, wf.log)
			},
			summary: func(count int) string { return fmt.Sprintf("Proposed destinations for %d files", count) },
		},
	}
}

// run executes one phase and logs its start and end. The error names the
// phase and wraps the cause, so context.Canceled stays visible to errors.Is.
func (wf *Workflow) run(ctx context.Context, phase workflowPhase) (int, error) {
	message := phaseMessageByKind[phase.kind]
	if message == "" {
		message = "Working…"
	}

	wf.log.Info(message, logger.UserKey, true,
		logger.PhaseKey, string(phase.kind), logger.EventKey, "start")
	start := time.Now()
	count, err := phase.run(ctx)
	elapsed := time.Since(start)
	if errors.Is(err, context.Canceled) {
		return count, fmt.Errorf("pipeline cancelled during %s phase: %w", phase.kind, err)
	}
	if err != nil {
		return count, fmt.Errorf("%s phase failed: %w", phase.kind, err)
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

	return count, nil
}
