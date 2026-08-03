// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package workflow

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func newTestWorkflow(t *testing.T, ctx context.Context) (*Workflow, *db.DB) {
	t.Helper()
	d := dbtest.New(t)
	cfg := &config.Configuration{
		Workers:   2,
		AppDBPath: filepath.Join(t.TempDir(), ".wandersort.db"),
	}
	wf := NewWorkflow(ctx, d, logger.NewNoopLogger(), cfg, Deps{})
	return wf, d
}

func TestWorkflowRunPhaseSuccess(t *testing.T) {
	wf, _ := newTestWorkflow(t, context.Background())

	phase := workflowPhase{
		kind:    workflowPhaseScan,
		run:     func() (int, error) { return 5, nil },
		summary: func(count int) string { return "did " + string(rune('0'+count)) },
	}

	count, status, errStr, ok := wf.run(phase)
	if !ok {
		t.Fatalf("expected ok=true, got false (status=%s err=%v)", status, errStr)
	}
	if count != 5 {
		t.Errorf("count: got %d, want 5", count)
	}
	if status != "" || errStr != nil {
		t.Errorf("success run should report no status/error, got status=%q err=%v", status, errStr)
	}
}

func TestWorkflowRunPhaseError(t *testing.T) {
	wf, _ := newTestWorkflow(t, context.Background())

	phase := workflowPhase{
		kind: workflowPhaseHash,
		run:  func() (int, error) { return 0, errors.New("disk full") },
	}

	_, status, errStr, ok := wf.run(phase)
	if ok {
		t.Fatal("expected ok=false on phase error")
	}
	if status != db.StatusFailed {
		t.Errorf("status: got %q, want %q", status, db.StatusFailed)
	}
	if errStr == nil {
		t.Fatal("expected non-nil error string")
	}
	if want := "hash phase failed: disk full"; *errStr != want {
		t.Errorf("errStr: got %q, want %q", *errStr, want)
	}
}

func TestWorkflowRunPhaseCanceled(t *testing.T) {
	wf, _ := newTestWorkflow(t, context.Background())

	phase := workflowPhase{
		kind: workflowPhaseExif,
		run:  func() (int, error) { return 0, context.Canceled },
	}

	_, status, errStr, ok := wf.run(phase)
	if ok {
		t.Fatal("expected ok=false on cancellation")
	}
	if status != db.StatusCancelled {
		t.Errorf("status: got %q, want %q", status, db.StatusCancelled)
	}
	if errStr == nil || *errStr != "pipeline cancelled during exif phase" {
		t.Errorf("errStr: got %v, want %q", errStr, "pipeline cancelled during exif phase")
	}
}

func TestRunScanReturnsContextCanceledWithoutRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wf, _ := newTestWorkflow(t, ctx)

	roots, err := wf.RunScan([]string{"/some/path"})
	if roots != nil {
		t.Errorf("roots: got %v, want nil", roots)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err: got %v, want context.Canceled", err)
	}
}

// TestWorkflowPhasesOrderAndMessages pins the phase pipeline order
// (scan→hash→exif→score→vfs) and that every phase kind has a start message —
// a phase with no entry in phaseMessageByKind silently falls back to the
// generic "Working…" line, which would go unnoticed without this check.
func TestWorkflowPhasesOrderAndMessages(t *testing.T) {
	wf, _ := newTestWorkflow(t, context.Background())

	phases := wf.workflowPhases([]string{"/root"})

	wantKinds := []workflowPhaseKind{
		workflowPhaseScan, workflowPhaseHash, workflowPhaseExif, workflowPhaseScore, workflowPhaseVFS,
	}
	if len(phases) != len(wantKinds) {
		t.Fatalf("phase count: got %d, want %d", len(phases), len(wantKinds))
	}
	for i, k := range wantKinds {
		if phases[i].kind != k {
			t.Errorf("phase %d: got kind %q, want %q", i, phases[i].kind, k)
		}
		if _, ok := phaseMessageByKind[phases[i].kind]; !ok {
			t.Errorf("phase %q has no entry in phaseMessageByKind", phases[i].kind)
		}
		if phases[i].summary == nil {
			t.Errorf("phase %q has no summary func", phases[i].kind)
		}
	}
}

// TestPhaseSummaryFormatting pins the exact wording of each phase's
// user-facing summary line — these are read by real users in the console/TUI,
// so a silent format change should fail a test, not just look different.
func TestPhaseSummaryFormatting(t *testing.T) {
	wf, _ := newTestWorkflow(t, context.Background())
	phases := wf.workflowPhases([]string{"/root"})

	want := map[workflowPhaseKind]string{
		workflowPhaseScan:  "Scanned 3 files",
		workflowPhaseHash:  "Checked 3 files for duplicates",
		workflowPhaseExif:  "Read details from 3 files",
		workflowPhaseScore: "Reviewed 3 duplicate groups",
		workflowPhaseVFS:   "Proposed destinations for 3 files",
	}
	for _, p := range phases {
		got := p.summary(3)
		if got != want[p.kind] {
			t.Errorf("%s summary: got %q, want %q", p.kind, got, want[p.kind])
		}
	}
}

// TestExifPhaseWrapsExiftoolDepsError pins the "exiftool: %w" wrapping the
// exif phase applies when the download dependency never became ready — the
// phase must fail rather than run exif.New with an empty path.
func TestExifPhaseWrapsExiftoolDepsError(t *testing.T) {
	wf, _ := newTestWorkflow(t, context.Background())
	wf.deps = Deps{
		Exiftool: func() (string, error) { return "", errors.New("download failed") },
	}

	phases := wf.workflowPhases([]string{"/root"})
	var exifPhase *workflowPhase
	for i := range phases {
		if phases[i].kind == workflowPhaseExif {
			exifPhase = &phases[i]
		}
	}
	if exifPhase == nil {
		t.Fatal("no exif phase found")
	}

	_, err := exifPhase.run()
	if err == nil || err.Error() != "exiftool: download failed" {
		t.Errorf("got %v, want wrapped \"exiftool: download failed\"", err)
	}
}

// TestVFSPhaseWrapsLocationDepsError mirrors the exif case for the vfs
// phase's "location resolver: %w" wrap.
func TestVFSPhaseWrapsLocationDepsError(t *testing.T) {
	wf, _ := newTestWorkflow(t, context.Background())
	wf.deps = Deps{
		Location: func() (*location.Resolver, error) { return nil, errors.New("download failed") },
	}

	phases := wf.workflowPhases([]string{"/root"})
	var vfsPhase *workflowPhase
	for i := range phases {
		if phases[i].kind == workflowPhaseVFS {
			vfsPhase = &phases[i]
		}
	}
	if vfsPhase == nil {
		t.Fatal("no vfs phase found")
	}

	_, err := vfsPhase.run()
	if err == nil || err.Error() != "location resolver: download failed" {
		t.Errorf("got %v, want wrapped \"location resolver: download failed\"", err)
	}
}

// TestFinalizeSessionLogsOutcome is a trivial-looking function, but it's the
// one place a run's terminal status/error actually gets logged — a silent
// signature change here (e.g. swapping which log level success uses) would
// otherwise go unnoticed since nothing else calls it.
func TestFinalizeSessionLogsOutcome(t *testing.T) {
	wf, _ := newTestWorkflow(t, context.Background())

	// Neither call should panic; the no-op logger discards everything, so
	// this only pins that both branches (error present / absent) run cleanly.
	errStr := "boom"
	wf.finalizeSession(db.StatusFailed, &errStr)
	wf.finalizeSession(db.StatusCompleted, nil)
}
