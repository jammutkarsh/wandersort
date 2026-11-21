package library

import (
	"context"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"

	"github.com/jammutkarsh/wandersort/pkg/core/verify"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/preview"
	"github.com/jammutkarsh/wandersort/pkg/report"
)

// Scan adds paths to the library, opening it first: walk, read, then a plan
// for everything not yet placed. It runs as JobScan (see StartJob), so no copy,
// other scan or re-plan overlaps it, and reads the database, folder and
// settings only once it holds the library. Once it returns, Review is the
// review of the plan it made (vfs.ErrNothingNew when nothing is left to
// copy); a review asked for while it runs is refused as busy.
func (s *Session) Scan(ctx context.Context, paths []string, force bool, deps workflow.Deps) (workflow.Result, error) {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	done, err := s.Work.StartJob(JobScan)
	if err != nil {
		return workflow.Result{}, err
	}
	defer done()
	if err := s.Open(ctx); err != nil {
		return workflow.Result{}, err
	}
	res, err := workflow.NewWorkflow(s.DB(), s.log, s.Configuration(), deps).RunScan(ctx, paths, force)
	s.RememberSources(res.Roots)
	return res, err
}

// CopyResult is what a copy did and what it left out.
type CopyResult struct {
	// NotRead is every source no scan could read, so no copy brings it in.
	NotRead []string
	// Failures groups the files left out by reason; empty when none were.
	Failures report.FailureReport
	// Page is the failure page written beside this run's log, "" when none.
	Page string
	execute.Report
}

// Copy applies the review's edits and copies every pending file into the
// library, opening it first (see execute.Run). It runs as JobCopy, so no scan
// or re-plan changes the plan under it, and reads the database and folder only
// once it holds the library. A real run that leaves files out writes the
// failure page; a dry run writes nothing.
func (s *Session) Copy(ctx context.Context, o execute.Options) (CopyResult, error) {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	done, err := s.Work.StartJob(JobCopy)
	if err != nil {
		return CopyResult{}, err
	}
	defer done()
	if err := s.Open(ctx); err != nil {
		return CopyResult{}, err
	}
	d, outputDir := s.DB(), s.OutputDir()
	applied := o.OnApplied
	o.OnApplied = func() {
		// the peeks showed the plan before its edits; nothing is left to peek at
		if err := preview.Clean(outputDir); err != nil {
			s.log.Warn("could not remove the preview copies", "error", err)
		}
		if applied != nil {
			applied()
		}
	}
	// read first, so a copy that stops still says what it could never bring in
	var res CopyResult
	if res.NotRead, err = execute.LeftBehind(ctx, d); err != nil {
		return res, err
	}
	if res.Report, err = execute.Run(ctx, d, s.log, outputDir, o); err != nil {
		return res, err
	}
	if o.DryRun || res.Failed+len(res.NotRead) == 0 {
		return res, nil
	}
	if res.Failures, err = report.Failures(ctx, d.SQL); err != nil {
		s.log.Warn("could not list the files left out", "error", err)
		return res, nil
	}
	res.Page = s.saveFailurePage(res.Failures, outputDir)
	return res, nil
}

// saveFailurePage writes failures beside this run's log and returns its path;
// "" when file logging is off or the write failed (logged).
func (s *Session) saveFailurePage(failures report.FailureReport, outputDir string) string {
	page := s.logFile.Page()
	if page == "" {
		return ""
	}
	if err := report.SaveHTML(page, failures, report.PageInfo{
		Library: outputDir, When: time.Now(), LogPath: s.logFile.Path(),
	}); err != nil {
		s.log.Warn("could not write the failure report", "error", err)
		return ""
	}
	s.log.Info("Report: "+page, logger.UserKey, true)
	return page
}

// Discard takes every file not yet copied off the list (see Discard), opening
// the library first, holding it from all other work; it refuses while a job
// or re-plan runs (ErrBusy).
func (s *Session) Discard(ctx context.Context) (int, error) {
	stop, ok := s.Work.Start()
	if !ok {
		return 0, ErrClosing
	}
	defer stop()
	release, ok := s.Work.HoldLibrary()
	if !ok {
		return 0, ErrBusy
	}
	defer release()
	if err := s.openExisting(ctx); err != nil {
		return 0, err
	}
	d, outputDir := s.DB(), s.OutputDir()
	defer s.Work.plan.Add(1)
	return discard(ctx, d, outputDir)
}

// Reset empties the library (see reset), opening it first, and clears the
// preview copies, holding the library from all other work; it refuses while a
// job or re-plan runs (ErrBusy).
func (s *Session) Reset(ctx context.Context) error {
	stop, ok := s.Work.Start()
	if !ok {
		return ErrClosing
	}
	defer stop()
	release, ok := s.Work.HoldLibrary()
	if !ok {
		return ErrBusy
	}
	defer release()
	if err := s.openExisting(ctx); err != nil {
		return err
	}
	d, outputDir := s.DB(), s.OutputDir()
	defer s.Work.plan.Add(1)
	if err := reset(ctx, d, outputDir, s.log); err != nil {
		return err
	}
	// preview copies outlive a session, so a wipe has to take them too
	if err := preview.Clean(outputDir); err != nil {
		s.log.Warn("could not remove the preview copies", "error", err)
	}
	return nil
}

// Check verifies the files placed in the library, opening it first (see
// verify.Run). It runs as JobCheck, since it forgets records of files gone
// from the library: never beside a scan, a copy or a re-plan.
func (s *Session) Check(ctx context.Context, o verify.Options) (verify.Report, error) {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	done, err := s.Work.StartJob(JobCheck)
	if err != nil {
		return verify.Report{}, err
	}
	defer done()
	if err := s.Open(ctx); err != nil {
		return verify.Report{}, err
	}
	return verify.Run(ctx, s.DB(), s.log, s.OutputDir(), o)
}

// CopyPlan is what a copy would do now: the files and bytes still to copy,
// the review edits it applies first, and the room for them.
type CopyPlan struct {
	Files int
	Bytes int64
	Edits int
	Space execute.Space
}

// CopyPlan reads what a copy of the library would do, opening it first, while
// the library can't be switched under the read.
func (s *Session) CopyPlan(ctx context.Context) (CopyPlan, error) {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	done, ok := s.Work.StartOnLibrary()
	if !ok {
		return CopyPlan{}, ErrClosing
	}
	defer done()
	if err := s.openExisting(ctx); err != nil {
		return CopyPlan{}, err
	}
	d, outputDir := s.DB(), s.OutputDir()
	var p CopyPlan
	var err error
	if p.Files, p.Bytes, err = execute.Pending(ctx, d); err != nil {
		return CopyPlan{}, err
	}
	if p.Space, err = execute.SpaceFor(ctx, d, outputDir); err != nil {
		return CopyPlan{}, err
	}
	if p.Edits, err = vfs.CountEdits(outputDir); err != nil {
		p.Edits = 1 // unreadable: something is there, and the copy will say what
	}
	return p, nil
}
