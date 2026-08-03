// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package lock is wandersort's file locking for scan/review coordination:
// acquire mechanics plus the domain lock filenames. A real OS advisory lock
// (tryFlock), not a PID file — see tryLock.
package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	OutputFileName  = ".wandersort.lock"
	InstallFileName = ".wandersort-install.lock"

	// Polling (not one OS-blocking wait) is what lets blocking acquire still
	// honour ctx cancellation — a blocking flock syscall can't be interrupted.
	pollInterval    = 200 * time.Millisecond
	pollMaxInterval = 2 * time.Second
)

// ErrHeld reports that another process currently owns the lock.
var ErrHeld = errors.New("lock held by another process")

// Lock is an exclusive OS advisory lock on a file within a directory.
type Lock struct {
	file *os.File
}

// Unlock releases the lock by closing the underlying file descriptor — the
// OS releases the lock itself at that point. Safe to call multiple times or
// on nil.
func (l *Lock) Unlock() {
	if l == nil || l.file == nil {
		return
	}
	l.file.Close()
	l.file = nil
}

// AcquireOutput takes the exclusive output-dir lock used by scan and review.
// A held lock comes back as *AlreadyRunningError, so the caller can style the
// message however it presents errors — this package renders nothing.
func AcquireOutput(dir string) (*Lock, error) {
	lockPath := filepath.Join(dir, OutputFileName)
	l, err := acquire(context.Background(), dir, OutputFileName, false)
	if errors.Is(err, ErrHeld) {
		pid, _ := readLockPID(lockPath)
		return nil, &AlreadyRunningError{PID: pid}
	}
	return l, err
}

// AcquireInstall takes the install-coordination lock shared by scan, review,
// and the config wizard, so only one downloads dependencies at a time.
// block=true waits for an in-progress install; false returns ErrHeld at once.
func AcquireInstall(ctx context.Context, dir string, block bool) (*Lock, error) {
	return acquire(ctx, dir, InstallFileName, block)
}

// AlreadyRunningError reports that a live wandersort process already holds the
// output-dir lock. It carries the holder's PID rather than a rendered message:
// styling belongs to whatever is presenting the error.
type AlreadyRunningError struct{ PID int }

func (e *AlreadyRunningError) Error() string {
	return fmt.Sprintf("another wandersort process is already running (PID %d)", e.PID)
}

func (e *AlreadyRunningError) Unwrap() error { return ErrHeld }

// acquire opens (creating if needed) dir/name and takes an OS advisory lock
// on it. When block is true it retries a non-blocking attempt until it
// succeeds or ctx is cancelled, backing off between tries; when false it
// returns ErrHeld at once.
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

// readLockPID returns the PID recorded in the lock file at path, for
// alreadyRunningError's message only — display, not the locking mechanism.
func readLockPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// tryLock attempts one non-blocking OS advisory lock on lockPath and, on
// success, stamps it with this process's PID for alreadyRunningError only.
func tryLock(lockPath string) (*Lock, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", lockPath, err)
	}

	if err := tryFlock(f); err != nil {
		f.Close()
		if errors.Is(err, ErrHeld) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("lock file %s: %w", lockPath, err)
	}

	if err := f.Truncate(0); err == nil {
		if _, err := f.Seek(0, 0); err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
		}
	}

	return &Lock{file: f}, nil
}
