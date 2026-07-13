package utils

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
)

// ErrLockHeld reports that a live process currently owns the lock.
var ErrLockHeld = errors.New("lock held by another process")

// lockPollInterval is how often a blocking Acquire re-checks a held lock.
const lockPollInterval = 200 * time.Millisecond

// Lock is an exclusive PID-based lock on a file within a directory.
type Lock struct {
	path string
}

// Acquire creates dir/name atomically via O_EXCL and records the caller's PID.
// It returns ErrLockHeld if a live process already owns the lock; a stale lock
// (dead PID) is reclaimed. When block is true it waits for the lock to free,
// polling until ctx is cancelled; when false it returns ErrLockHeld at once.
func Acquire(ctx context.Context, dir, name string, block bool) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory %s: %w", dir, err)
	}

	lockPath := filepath.Join(dir, name)
	for {
		lock, err := tryLock(lockPath)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, ErrLockHeld) {
			return nil, err
		}
		if !block {
			return nil, ErrLockHeld
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

// Unlock removes the lock file. Safe to call multiple times or on nil.
func (l *Lock) Unlock() {
	if l == nil || l.path == "" {
		return
	}
	os.Remove(l.path)
	l.path = ""
}

// ReadLockPID returns the PID recorded in the lock file at path.
func ReadLockPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// tryLock attempts a single exclusive create, reclaiming a stale (dead PID) lock.
func tryLock(lockPath string) (*Lock, error) {
	if pid, err := ReadLockPID(lockPath); err == nil && processExists(pid) {
		return nil, ErrLockHeld
	}
	os.Remove(lockPath) // clear a stale lock before the exclusive create

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrLockHeld // lost the race to another process
		}
		return nil, fmt.Errorf("create lock file %s: %w", lockPath, err)
	}

	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		os.Remove(lockPath)
		return nil, fmt.Errorf("write lock file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(lockPath)
		return nil, fmt.Errorf("close lock file: %w", err)
	}

	return &Lock{path: lockPath}, nil
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks existence without sending a real signal (Unix)
	return process.Signal(syscall.Signal(0)) == nil
}
