// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package lock

import (
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
		{"AcquireOutput_UnlockReleasesForNextCaller", func(t *testing.T) {
			dir := t.TempDir()

			l1, err := AcquireOutput(dir)
			if err != nil {
				t.Fatalf("first acquire: %v", err)
			}
			l1.Unlock()

			l2, err := AcquireOutput(dir)
			if err != nil {
				t.Fatalf("acquire after Unlock should succeed, got: %v", err)
			}
			l2.Unlock()
		}},
		{"AcquireOutput_IgnoresStaleFileContent", func(t *testing.T) {
			// The lock is enforced by the OS, not by the file's bytes — a
			// PID left over from an old on-disk format, or one that can
			// never belong to a live process, must not matter either way.
			dir := t.TempDir()
			lockPath := filepath.Join(dir, OutputFileName)
			if err := os.WriteFile(lockPath, []byte(strconv.Itoa(1<<30)), 0o644); err != nil {
				t.Fatalf("seed lock file: %v", err)
			}

			l, err := AcquireOutput(dir)
			if err != nil {
				t.Fatalf("acquire over a file with stale content: %v", err)
			}
			l.Unlock()
		}},
		{"Unlock_Idempotent", func(t *testing.T) {
			dir := t.TempDir()

			l, err := AcquireOutput(dir)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			l.Unlock()
			l.Unlock() // must not panic

			var nilLock *Lock
			nilLock.Unlock() // must not panic
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
