// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package exiftool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPoolStartsAndClosesConcurrently covers the concurrent NewPool/Close
// path: every worker must come up and every worker must shut down, even
// though both now happen in parallel goroutines rather than one at a time.
func TestPoolStartsAndClosesConcurrently(t *testing.T) {
	const size = 4
	p, err := NewPool("exiftool", size)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	if got := len(p.workers); got != size {
		t.Fatalf("NewPool started %d workers, want %d", got, size)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestNewPoolAggregatesStartupErrors covers the failure path: if a worker
// fails to start, NewPool must still return an error (not hang, not report
// success with a partially-started pool).
func TestNewPoolAggregatesStartupErrors(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "no-such-exiftool")
	if _, err := NewPool(bogus, 4); err == nil {
		t.Fatal("NewPool with a nonexistent binary = nil error, want one")
	}
}

// TestPoolExtractUsesConcurrentlyStartedWorkers is a smoke test that a
// worker started by the concurrent NewPool actually works.
func TestPoolExtractUsesConcurrentlyStartedWorkers(t *testing.T) {
	p, err := NewPool("exiftool", 2)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer p.Close()

	if _, err := p.Extract(context.Background(), filepath.Join(t.TempDir(), "missing.jpg")); err == nil {
		t.Fatal("Extract on a missing file = nil error, want one")
	}
}

// hangingExiftool is a stand-in binary that accepts arguments and never
// answers, like exiftool stuck in a malformed file. Its stdout stays open
// (a `cat >/dev/null` would close it and read as the process dying).
func hangingExiftool(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "exiftool")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nwhile read -r l; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractRejectsLineBreaksInPath(t *testing.T) {
	e, err := New(hangingExiftool(t))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, p := range []string{"/a/x.jpg\n-all=\n-overwrite_original\n/a/y.jpg", "/a/x\r.jpg"} {
		if _, err := e.Extract(context.Background(), p); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("Extract(%q) = %v, want ErrUnsafePath", p, err)
		}
	}
	if e.Dead() {
		t.Error("an unsafe path must not cost the worker")
	}
}

func TestExtractTimeoutKillsWorker(t *testing.T) {
	old := extractTimeout
	extractTimeout = 200 * time.Millisecond
	t.Cleanup(func() { extractTimeout = old })

	e, err := New(hangingExiftool(t))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	start := time.Now()
	if _, err := e.Extract(context.Background(), "/a/x.jpg"); !errors.Is(err, ErrProcess) {
		t.Fatalf("Extract = %v, want ErrProcess", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("Extract did not return promptly after the timeout")
	}
	if !e.Dead() {
		t.Fatal("a timed-out worker must be marked dead")
	}
	if _, err := e.Extract(context.Background(), "/a/x.jpg"); !errors.Is(err, ErrProcess) {
		t.Fatalf("Extract on a dead worker = %v, want ErrProcess", err)
	}
}

func TestExtractCancelKillsWorker(t *testing.T) {
	e, err := New(hangingExiftool(t))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := e.Extract(ctx, "/a/x.jpg"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Extract = %v, want the context's error", err)
	}
}

func TestPoolReplacesDeadWorker(t *testing.T) {
	old := extractTimeout
	extractTimeout = 100 * time.Millisecond
	t.Cleanup(func() { extractTimeout = old })

	p, err := NewPool(hangingExiftool(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Extract(context.Background(), "/a/x.jpg") // kills the only worker
	first := <-p.workers
	p.workers <- first
	if !first.Dead() {
		t.Fatal("setup: worker should be dead")
	}
	p.Extract(context.Background(), "/a/y.jpg")
	second := <-p.workers
	p.workers <- second
	if second == first {
		t.Fatal("the pool handed out a dead worker instead of starting a new one")
	}
}
