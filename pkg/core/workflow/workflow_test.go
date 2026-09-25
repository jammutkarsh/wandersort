// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func newTestWorkflow(t *testing.T) (*Workflow, *db.DB) {
	t.Helper()
	d := dbtest.New(t)
	cfg := &config.Configuration{
		Workers:   2,
		AppDBPath: filepath.Join(t.TempDir(), ".wandersort.db"),
	}
	wf := NewWorkflow(d, logger.NewNoopLogger(), cfg, Deps{})
	return wf, d
}

func TestWorkflowRunPhase(t *testing.T) {
	tests := []struct {
		name      string
		run       func(context.Context) (int, error)
		wantCount int
		wantErr   string
		wantIs    error
	}{
		{"success", func(context.Context) (int, error) { return 5, nil }, 5, "", nil},
		{
			"failure names the phase and keeps the cause",
			func(context.Context) (int, error) { return 0, errDiskFull },
			0, "metadata phase failed: disk full", errDiskFull,
		},
		{
			"cancellation stays visible to errors.Is",
			func(context.Context) (int, error) { return 0, context.Canceled },
			0, "pipeline cancelled during metadata phase: context canceled", context.Canceled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf, _ := newTestWorkflow(t)
			count, err := wf.run(context.Background(), workflowPhase{kind: workflowPhaseMetadata, run: tt.run})
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("err = %v, want %q", err, tt.wantErr)
			}
			if !errors.Is(err, tt.wantIs) {
				t.Errorf("errors.Is(err, %v) = false", tt.wantIs)
			}
		})
	}
}

var errDiskFull = errors.New("disk full")

func TestRunScanReturnsContextCanceledWithoutRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wf, _ := newTestWorkflow(t)

	roots, err := wf.RunScan(ctx, []string{"/some/path"}, false)
	if roots != nil {
		t.Errorf("roots: got %v, want nil", roots)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err: got %v, want context.Canceled", err)
	}
}

// A scan root that is the library, holds it, or sits in it is refused before
// anything is walked; a sibling folder is fine.
func TestRunScanRefusesRootsOverlappingTheLibrary(t *testing.T) {
	base := t.TempDir()
	library := filepath.Join(base, "library")
	for _, d := range []string{"library/2024", "photos", "library-old"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name    string
		root    string
		refused bool
	}{
		{"the library itself", library, true},
		{"a folder holding the library", base, true},
		{"a folder inside the library", filepath.Join(library, "2024"), true},
		{"a sibling whose name starts the same", filepath.Join(base, "library-old"), false},
	}
	d := dbtest.New(t)
	cfg := &config.Configuration{Workers: 1, AppDBPath: filepath.Join(library, ".wandersort.db")}
	wf := NewWorkflow(d, logger.NewNoopLogger(), cfg, Deps{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := wf.path.RealPath(tt.root) // roots arrive canonical
			if err != nil {
				t.Fatal(err)
			}
			err = wf.checkOverlap([]string{root})
			if got := errors.Is(err, ErrOverlapsLibrary); got != tt.refused {
				t.Errorf("refused = %v (err %v), want %v", got, err, tt.refused)
			}
		})
	}

	// and RunScan asks before walking anything
	if _, err := wf.RunScan(context.Background(), []string{base}, false); !errors.Is(err, ErrOverlapsLibrary) {
		t.Errorf("RunScan over the library's parent = %v, want ErrOverlapsLibrary", err)
	}
}

// TestWorkflowPhasesOrderAndMessages pins the phase pipeline order
// (scan→metadata→vfs) and that every phase kind has a start message —
// a phase with no entry in phaseMessageByKind silently falls back to the
// generic "Working…" line, which would go unnoticed without this check.
func TestWorkflowPhasesOrderAndMessages(t *testing.T) {
	wf, _ := newTestWorkflow(t)

	phases := wf.workflowPhases([]string{"/root"}, false)

	wantKinds := []workflowPhaseKind{
		workflowPhaseScan, workflowPhaseMetadata, workflowPhaseVFS,
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
	wf, _ := newTestWorkflow(t)
	phases := wf.workflowPhases([]string{"/root"}, false)

	want := map[workflowPhaseKind]string{
		workflowPhaseScan:     "Scanned 3 files",
		workflowPhaseMetadata: "Read 3 files",
		workflowPhaseVFS:      "Proposed destinations for 3 files",
	}
	for _, p := range phases {
		got := p.summary(3)
		if got != want[p.kind] {
			t.Errorf("%s summary: got %q, want %q", p.kind, got, want[p.kind])
		}
	}
}

// TestMetadataPhaseWrapsExiftoolDepsError pins the "exiftool: %w" wrapping the
// metadata phase applies when the download dependency never became ready — the
// phase must fail rather than run metadata.New with an empty path.
func TestMetadataPhaseWrapsExiftoolDepsError(t *testing.T) {
	wf, _ := newTestWorkflow(t)
	wf.deps = Deps{
		Exiftool: func() (string, error) { return "", errors.New("download failed") },
	}

	phases := wf.workflowPhases([]string{"/root"}, false)
	var metaPhase *workflowPhase
	for i := range phases {
		if phases[i].kind == workflowPhaseMetadata {
			metaPhase = &phases[i]
		}
	}
	if metaPhase == nil {
		t.Fatal("no metadata phase found")
	}

	_, err := metaPhase.run(context.Background())
	if err == nil || err.Error() != "exiftool: download failed" {
		t.Errorf("got %v, want wrapped \"exiftool: download failed\"", err)
	}
}

// TestVFSPhaseWrapsLocationDepsError mirrors the metadata case for the vfs
// phase's "location resolver: %w" wrap.
func TestVFSPhaseWrapsLocationDepsError(t *testing.T) {
	wf, _ := newTestWorkflow(t)
	wf.deps = Deps{
		Location: func() (*location.Resolver, error) { return nil, errors.New("download failed") },
	}

	phases := wf.workflowPhases([]string{"/root"}, false)
	var vfsPhase *workflowPhase
	for i := range phases {
		if phases[i].kind == workflowPhaseVFS {
			vfsPhase = &phases[i]
		}
	}
	if vfsPhase == nil {
		t.Fatal("no vfs phase found")
	}

	_, err := vfsPhase.run(context.Background())
	if err == nil || err.Error() != "location resolver: download failed" {
		t.Errorf("got %v, want wrapped \"location resolver: download failed\"", err)
	}
}
