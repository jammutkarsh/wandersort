// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package lock

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLock(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"AcquireOutput_SecondCallerIsHeld", func(t *testing.T) {
			dir := t.TempDir()

			l1, err := AcquireOutput(dir)
			if err != nil {
				t.Fatalf("first acquire: %v", err)
			}
			defer l1.Unlock()

			if _, err := AcquireOutput(dir); err == nil {
				t.Fatal("second acquire on the same dir should fail while the first is held")
			}
		}},
		{"AcquireOutput_ReclaimsStaleLock", func(t *testing.T) {
			dir := t.TempDir()
			lockPath := filepath.Join(dir, OutputFileName)

			// A PID that cannot belong to a live process.
			if err := os.WriteFile(lockPath, []byte(strconv.Itoa(1<<30)), 0o644); err != nil {
				t.Fatalf("seed stale lock: %v", err)
			}

			l, err := AcquireOutput(dir)
			if err != nil {
				t.Fatalf("acquire over stale lock: %v", err)
			}
			l.Unlock()

			if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
				t.Fatalf("Unlock should remove the reclaimed lock file, stat err = %v", err)
			}
		}},
		{"Unlock_DoesNotRemoveAnotherOwnersLock", func(t *testing.T) {
			dir := t.TempDir()

			l, err := acquire(context.Background(), dir, OutputFileName, false)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}

			// Simulate another process having reclaimed the same path after l was
			// created (the scenario the create-first tryLock ordering guards
			// against): the lock file now records a different PID.
			lockPath := l.path
			if err := os.WriteFile(lockPath, []byte(strconv.Itoa(l.pid+1)), 0o644); err != nil {
				t.Fatalf("rewrite lock file: %v", err)
			}

			l.Unlock()

			if _, err := os.Stat(lockPath); err != nil {
				t.Fatalf("Unlock must not remove a lock file it no longer owns: stat err = %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
