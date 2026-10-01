package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/metadata"
	"github.com/jammutkarsh/wandersort/pkg/core/scanner"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// Deps supplies the two downloadable dependencies, blocking until each is
// ready.
type Deps struct {
	Exiftool func(context.Context) (string, error)             // path to the exiftool binary
	Location func(context.Context) (*location.Resolver, error) // open geonames resolver
}

// ErrOverlapsLibrary means a folder to scan is, holds, or sits inside the
// library.
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
	// starting is the phase's one user-facing start line
	starting string
	run      func(ctx context.Context) (int, error)
	// summary is the phase's one user-facing success line; elapsed time is
	// appended to it
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

func NewWorkflow(db *db.DB, log logger.Logger, cfg *config.Configuration, deps Deps) *Workflow {
	vfsCfg := vfs.ConfigFor(cfg)
	// show the output folder and rules up front: they come from flags/history
	// and the library's settings
	rules := "none (flat Year/Month)"
	if len(vfsCfg.Rules) > 0 {
		rules = strings.Join(vfsCfg.Rules, ", ")
	}
	log.Info("Pipeline configured",
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

// Result is what one scan did: the roots walked and each phase's count.
type Result struct {
	Roots   []string
	Found   int // files the walk saw
	Read    int // files hashed and read this run
	Planned int // files the plan proposes a place for
}

// RunScan canonicalizes and prunes nested roots, then runs the pipeline
// synchronously. A stopped run's error wraps context.Canceled. force re-reads
// every file even if unchanged.
func (wf *Workflow) RunScan(ctx context.Context, paths []string, force bool) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	roots, err := path.ReduceRoots(wf.path, paths)
	if err != nil {
		wf.log.Warn("Invalid scan roots", "error", err)
		return Result{}, err
	}
	if err := wf.checkOverlap(roots); err != nil {
		return Result{}, err
	}

	storedPaths := make([]string, 0, len(roots))
	for _, p := range roots {
		storedPaths = append(storedPaths, wf.path.RelativeToHome(p))
	}
	wf.log.Info("Starting scan", "paths", storedPaths)

	res := Result{Roots: roots}
	if err := wf.runPhases(ctx, &res, force); err != nil {
		wf.log.Error("Pipeline finished", "error", err)
		return res, err
	}
	wf.log.Info("Pipeline finished")
	return res, nil
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
func (wf *Workflow) runPhases(ctx context.Context, res *Result, force bool) error {
	wf.log.Info("Workflow started", "phases", "scanning → reading → organizing")
	counts := map[workflowPhaseKind]*int{
		workflowPhaseScan:     &res.Found,
		workflowPhaseMetadata: &res.Read,
		workflowPhaseVFS:      &res.Planned,
	}
	for _, phase := range wf.workflowPhases(res.Roots, force) {
		n, err := wf.run(ctx, phase)
		*counts[phase.kind] = n
		if err != nil {
			return err
		}
	}
	// last thing before the run is done, so it sits next to the "review"
	// hint; the same check execute refuses on, so the two never disagree
	var full *execute.NotEnoughSpaceError
	if err := execute.CheckFits(ctx, wf.db, wf.outputDir); errors.As(err, &full) {
		wf.log.Warn(fmt.Sprintf("Output volume may be too small: copying the plan needs %s, only %s is free at %s",
			volume.HumanBytes(full.Needed), volume.HumanBytes(full.Free), wf.outputDir), logger.UserKey, true)
	} else if err != nil {
		wf.log.Warn("Could not check the output volume's free space", "error", err)
	}
	return nil
}

func (wf *Workflow) workflowPhases(paths []string, force bool) []workflowPhase {
	return []workflowPhase{
		{
			kind:     workflowPhaseScan,
			starting: "Finding your files…",
			run: func(ctx context.Context) (int, error) {
				return wf.scanner.Run(ctx, paths, force)
			},
			summary: func(count int) string { return "Found " + files(count) },
		},
		{
			kind:     workflowPhaseMetadata,
			starting: "Reading your files…",
			run: func(ctx context.Context) (int, error) {
				// blocks here (not at construction) if exiftool is still
				// downloading — the walk has already run meanwhile
				exiftoolPath, err := wf.deps.Exiftool(ctx)
				if err != nil {
					return 0, fmt.Errorf("exiftool: %w", err)
				}
				extractor, err := metadata.New(wf.db, wf.log, exiftoolPath, wf.workers)
				if err != nil {
					return 0, err
				}
				return extractor.Run(ctx)
			},
			summary: func(count int) string { return "Read " + files(count) },
		},
		{
			kind:     workflowPhaseVFS,
			starting: "Planning folders…",
			run: func(ctx context.Context) (int, error) {
				resolver, err := wf.deps.Location(ctx)
				if err != nil {
					return 0, fmt.Errorf("location resolver: %w", err)
				}
				return vfs.Propose(ctx, wf.db, resolver, wf.appCfg, wf.log)
			},
			summary: func(count int) string { return "Planned " + files(count) },
		},
	}
}

// run executes one phase and logs its start and end. The error names the
// phase and wraps the cause, so context.Canceled stays visible to errors.Is.
func (wf *Workflow) run(ctx context.Context, phase workflowPhase) (int, error) {
	wf.log.Info(phase.starting, logger.UserKey, true,
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

	// make this phase's writes visible to the next one; a lost write fails it
	if err := wf.db.Writer.Flush(); err != nil {
		return count, fmt.Errorf("%s phase failed: %w", phase.kind, err)
	}

	// checkpoint after every phase to keep the WAL small; not fatal
	if cpErr := wf.db.Checkpoint(); cpErr != nil {
		wf.log.Warn("Database checkpoint failed", "phase", string(phase.kind), "error", cpErr)
	}

	// one line per phase: what it did and how long it took
	msg := fmt.Sprintf("%s in %s", phase.summary(count), elapsed.Round(time.Millisecond))
	wf.log.Info(msg, logger.UserKey, true,
		logger.PhaseKey, string(phase.kind), logger.EventKey, "done",
		logger.ElapsedKey, elapsed.Round(time.Millisecond).String())

	return count, nil
}

// files counts files in a sentence: "1 file", "15,481 files".
func files(n int) string {
	if n == 1 {
		return "1 file"
	}
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return b.String() + " files"
}
