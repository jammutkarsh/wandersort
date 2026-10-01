// Package lock provides OS advisory file locks (released by the kernel when
// the holder exits) for the output dir and dependency installs.
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

// Unlock releases the lock by closing its file. Safe to call twice or on nil.
func (l *Lock) Unlock() {
	if l == nil || l.file == nil {
		return
	}
	l.file.Close()
	l.file = nil
}

// AcquireOutput takes the exclusive output-dir lock. A held lock is
// *AlreadyRunningError; this package renders nothing.
func AcquireOutput(dir string) (*Lock, error) {
	lockPath := filepath.Join(dir, OutputFileName)
	l, err := acquire(context.Background(), dir, OutputFileName, false)
	if errors.Is(err, ErrHeld) {
		pid, _ := readLockPID(lockPath)
		return nil, &AlreadyRunningError{PID: pid}
	}
	return l, err
}

// AcquireInstall takes the install lock so one process downloads dependencies
// at a time. block waits; otherwise ErrHeld comes back at once.
func AcquireInstall(ctx context.Context, dir string, block bool) (*Lock, error) {
	return acquire(ctx, dir, InstallFileName, block)
}

// AlreadyRunningError reports the PID of the process holding the output lock.
type AlreadyRunningError struct{ PID int }

func (e *AlreadyRunningError) Error() string {
	return fmt.Sprintf("another wandersort process is already running (PID %d)", e.PID)
}

func (e *AlreadyRunningError) Unwrap() error { return ErrHeld }

// acquire opens dir/name and takes an advisory lock on it, retrying with
// backoff until ctx is done when block is set.
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
