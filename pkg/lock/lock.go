// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package lock is wandersort's PID-based file locking: generic acquire/reclaim
// mechanics plus the domain-specific filenames and styled "already running"
// message for scan/serve coordination.
package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/tui"
)

const (
	OutputFileName  = ".wandersort.lock"
	InstallFileName = ".wandersort-install.lock"

	// pollInterval is the starting wait between re-checks of a held lock;
	// pollMaxInterval caps the exponential backoff applied on each retry.
	pollInterval    = 200 * time.Millisecond
	pollMaxInterval = 2 * time.Second
)

// ErrHeld reports that a live process currently owns the lock.
var ErrHeld = errors.New("lock held by another process")

// Lock is an exclusive PID-based lock on a file within a directory.
type Lock struct {
	path string
	pid  int
}

// Unlock removes the lock file, but only if it still records this process's
// PID — if another process reclaimed the path first (see tryLock), removing
// it here would release a lock we no longer hold. Safe to call multiple
// times or on nil.
func (l *Lock) Unlock() {
	if l == nil || l.path == "" {
		return
	}
	if pid, err := readLockPID(l.path); err != nil || pid != l.pid {
		l.path = ""
		return
	}
	os.Remove(l.path)
	l.path = ""
}

// AcquireOutput takes the exclusive output-dir lock used by scan and serve.
// It renders a helpful message when another live wandersort process holds it.
func AcquireOutput(dir string) (*Lock, error) {
	lockPath := filepath.Join(dir, OutputFileName)
	l, err := acquire(context.Background(), dir, OutputFileName, false)
	if errors.Is(err, ErrHeld) {
		pid, _ := readLockPID(lockPath)
		return nil, alreadyRunningError(lockPath, pid)
	}
	return l, err
}

// AcquireInstall takes the install-coordination lock shared by scan, serve,
// and review, so only one of them downloads dependencies at a time.
// When block is true it waits for an in-progress install to finish
// (EnsureDependencies); when false it returns ErrHeld at once so the caller
// can step aside (the non-blocking path).
func AcquireInstall(ctx context.Context, dir string, block bool) (*Lock, error) {
	return acquire(ctx, dir, InstallFileName, block)
}

// alreadyRunningError renders the user-facing message for a held output-dir lock.
func alreadyRunningError(lockPath string, pid int) error {
	msg := tui.Bad.Render(fmt.Sprintf("Another wandersort process is already running (PID %d).", pid)) + "\n\n" +
		tui.FaintTxt.Render("Only one scan or server can use the same output directory at a time.") + "\n" +
		tui.FaintTxt.Render("Stop the other process first, or if it already exited, remove the lock file:") + "\n" +
		tui.FaintTxt.Render("  rm "+lockPath)

	return fmt.Errorf("%s", msg)
}

// acquire creates dir/name atomically via O_EXCL and records the caller's PID.
// It returns ErrHeld if a live process already owns the lock; a stale lock
// (dead PID) is reclaimed. When block is true it waits for the lock to free,
// polling until ctx is cancelled; when false it returns ErrHeld at once.
func acquire(ctx context.Context, dir, name string, block bool) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory %s: %w", dir, err)
	}

	lockPath := filepath.Join(dir, name)
	wait := pollInterval
	for {
		l, err := tryLock(lockPath)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, ErrHeld) {
			return nil, err
		}
		if !block {
			return nil, ErrHeld
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		if wait < pollMaxInterval {
			wait *= 2
		}
	}
}

// readLockPID returns the PID recorded in the lock file at path.
func readLockPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// tryLock attempts a single exclusive create, reclaiming a stale (dead PID)
// lock. The create is attempted first and unconditionally: on the common
// path (no existing lock) this is a single atomic O_EXCL syscall with no
// intervening remove, so there is no window in which a concurrent holder's
// freshly-created lock can be deleted. Only when create fails because a
// lock file is already there do we inspect its PID, and only remove it if
// that owner is confirmed dead.
func tryLock(lockPath string) (*Lock, error) {
	if l, err := createLock(lockPath); err == nil {
		return l, nil
	} else if !os.IsExist(err) {
		return nil, fmt.Errorf("create lock file %s: %w", lockPath, err)
	}

	pid, err := readLockPID(lockPath)
	if err == nil && processExists(pid) {
		return nil, ErrHeld
	}
	os.Remove(lockPath) // stale: owner is dead (or file was unreadable)

	l, err := createLock(lockPath)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrHeld // lost the reclaim race to another process
		}
		return nil, fmt.Errorf("create lock file %s: %w", lockPath, err)
	}
	return l, nil
}

// createLock exclusively creates lockPath and writes the caller's PID into
// it. Returns an os.IsExist error if the path is already taken.
func createLock(lockPath string) (*Lock, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	pid := os.Getpid()
	if _, err := fmt.Fprintf(f, "%d\n", pid); err != nil {
		f.Close()
		os.Remove(lockPath)
		return nil, fmt.Errorf("write lock file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(lockPath)
		return nil, fmt.Errorf("close lock file: %w", err)
	}

	return &Lock{path: lockPath, pid: pid}, nil
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks existence without sending a real signal (Unix)
	return process.Signal(syscall.Signal(0)) == nil
}
