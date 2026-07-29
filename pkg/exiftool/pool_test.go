// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package exiftool

import (
	"context"
	"path/filepath"
	"testing"
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
