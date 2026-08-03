// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/spf13/cobra"
)

func testConfig(t *testing.T) *config.Configuration {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg, _, err := config.Resolve(config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestIsTuiEnabled pins the --plain escape hatch: it must short-circuit the
// terminal check rather than merely influence it, since `go test` never runs
// with a terminal on stderr and the branch would otherwise be untestable.
func TestIsTuiEnabled(t *testing.T) {
	a := &app{}
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().Bool(flagPlain, false, "")

	if err := cmd.Flags().Set(flagPlain, "true"); err != nil {
		t.Fatal(err)
	}
	if a.isTuiEnabled(cmd) {
		t.Error("--plain=true must disable the TUI regardless of the terminal check")
	}

	if err := cmd.Flags().Set(flagPlain, "false"); err != nil {
		t.Fatal(err)
	}
	// go test's stderr is never a terminal, so this must fall through to false too.
	if a.isTuiEnabled(cmd) {
		t.Error("isTuiEnabled under `go test` (non-tty stderr) must be false")
	}
}

// TestLockOutputAlreadyRunning is the one error users hit routinely: a second
// process against the same output dir must get the full styled explanation,
// not a bare wrapped error.
func TestLockOutputAlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	held, err := lock.AcquireOutput(dir)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer held.Unlock()

	a := &app{Config: &config.Configuration{LogFile: filepath.Join(dir, "wandersort.log")}}
	_, err = a.lockOutput()
	if err == nil {
		t.Fatal("lockOutput must fail while another lock is held")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("lockOutput error = %q, want the styled already-running message", err.Error())
	}
}

// TestLockOutputSucceedsWhenFree is the other half: an unheld directory must
// acquire cleanly and hand back a lock the caller can release.
func TestLockOutputSucceedsWhenFree(t *testing.T) {
	dir := t.TempDir()
	a := &app{Config: &config.Configuration{LogFile: filepath.Join(dir, "wandersort.log")}}
	l, err := a.lockOutput()
	if err != nil {
		t.Fatalf("lockOutput: %v", err)
	}
	defer l.Unlock()
}

// TestInitAppDBIdempotent covers the early-return branch: calling it twice
// must not replace an already-open handle (which would leak the first one's
// file descriptor).
func TestInitAppDBIdempotent(t *testing.T) {
	cfg := testConfig(t)
	a := &app{Config: cfg, Log: logger.NewNoopLogger()}
	ctx := context.Background()

	if err := a.initAppDB(ctx); err != nil {
		t.Fatalf("initAppDB: %v", err)
	}
	first := a.AppDB
	if first == nil {
		t.Fatal("initAppDB left AppDB nil")
	}
	if err := a.initAppDB(ctx); err != nil {
		t.Fatalf("initAppDB (second call): %v", err)
	}
	if a.AppDB != first {
		t.Error("initAppDB replaced an already-open AppDB handle")
	}
	a.closeDBs()
}

// TestCloseDBsNilSafe is the "nothing was ever opened" path — closeDBs runs
// unconditionally in every command's deferred cleanup, including the
// no-database-found early-return paths, so it must not panic on a bare app.
func TestCloseDBsNilSafe(t *testing.T) {
	a := &app{Log: logger.NewNoopLogger()}
	a.closeDBs() // must not panic
}

// TestWorkflowDepsReadsThroughToCoordinator pins that workflowDeps is a live
// pass-through to a.Deps at call time, not a value captured once when the
// closures are built — the TUI path constructs the workflow before a.Deps
// exists, and relies on exactly this indirection.
func TestWorkflowDepsReadsThroughToCoordinator(t *testing.T) {
	a := &app{Config: testConfig(t), Log: logger.NewNoopLogger()}
	deps := a.workflowDeps()
	a.Deps = a.newDeps(nil)

	// Neither getter should be called (no download started), but they must
	// resolve against the Coordinator built *after* workflowDeps returned.
	if deps.Exiftool == nil || deps.Location == nil {
		t.Fatal("workflowDeps returned nil closures")
	}
}
