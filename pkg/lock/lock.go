// Package lock provides OS advisory file locks (released by the kernel when
// the holder exits) for the output dir and dependency installs.
package lock

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	OutputFileName  = ".wandersort.lock"
	InstallFileName = ".wandersort-install.lock"
	HistoryFileName = ".wandersort-history.lock"

	// Polling (not one OS-blocking wait) is what lets blocking acquire still
	// honour ctx cancellation — a blocking flock syscall can't be interrupted.
	pollInterval    = 200 * time.Millisecond
	pollMaxInterval = 2 * time.Second

	// the history lock is held for one small file's replace: retry often
	historyPollInterval    = 2 * time.Millisecond
	historyPollMaxInterval = 50 * time.Millisecond
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
	l, err := acquire(context.Background(), dir, OutputFileName, false, pollInterval, pollMaxInterval)
	if errors.Is(err, ErrHeld) {
		pid, program := readLockHolder(lockPath)
		return nil, &AlreadyRunningError{PID: pid, Program: program}
	}
	return l, err
}

// AcquireInstall takes the install lock so one process downloads dependencies
// at a time. block waits; otherwise ErrHeld comes back at once.
func AcquireInstall(ctx context.Context, dir string, block bool) (*Lock, error) {
	return acquire(ctx, dir, InstallFileName, block, pollInterval, pollMaxInterval)
}

// AcquireHistory takes the lock on the app folder's history files, waiting
// for another holder. It is held only while a history is read or replaced.
func AcquireHistory(dir string) (*Lock, error) {
	return acquire(context.Background(), dir, HistoryFileName, true, historyPollInterval, historyPollMaxInterval)
}

// AlreadyRunningError reports the process holding the output lock: its PID
// and its program's name, each zero when unreadable.
type AlreadyRunningError struct {
	PID     int
	Program string
}

func (e *AlreadyRunningError) Error() string {
	program := e.Program
	if program == "" {
		program = "another program"
	}
	return fmt.Sprintf("%s already has this library open (PID %d)", program, e.PID)
}

func (e *AlreadyRunningError) Unwrap() error { return ErrHeld }

// acquire opens dir/name and takes an advisory lock on it, retrying with
// jittered backoff from wait up to maxWait until ctx is done when block is set.
func acquire(ctx context.Context, dir, name string, block bool, wait, maxWait time.Duration) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory %s: %w", dir, err)
	}

	lockPath := filepath.Join(dir, name)
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
		// jittered, so waiters that met once don't keep meeting
		case <-time.After(wait/2 + rand.N(wait)):
		}
		wait = min(2*wait, maxWait)
	}
}

// readLockHolder returns the PID and program recorded in the lock file at
// path, for AlreadyRunningError's message only — display, not the locking
// mechanism. Each is zero when unreadable (Windows can't read a locked file).
func readLockHolder(path string) (pid int, program string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	pidLine, program, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
	pid, _ = strconv.Atoi(strings.TrimSpace(pidLine))
	return pid, strings.TrimSpace(program)
}

// programName is this process's program as a person would name it: the
// executable's base name without its extension.
func programName() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe))
}

// tryLock attempts one non-blocking OS advisory lock on lockPath and, on
// success, stamps it with this process's PID and program for
// AlreadyRunningError only.
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
			fmt.Fprintf(f, "%d\n%s\n", os.Getpid(), programName())
		}
	}

	return &Lock{file: f}, nil
}
