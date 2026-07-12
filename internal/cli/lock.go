package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/charmbracelet/lipgloss"
)

const lockFileName = ".wandersort.lock"

// Lock represents an exclusive PID-based lock on the output directory.
type Lock struct {
	file *os.File
	path string
}

// AcquireLock creates a PID lock file in dir atomically via os.O_EXCL.
// Returns an error if the directory is already locked by a live process.
func AcquireLock(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	lockPath := filepath.Join(dir, lockFileName)

	// Check for stale lock
	if existingPID, err := readLockPID(lockPath); err == nil && processExists(existingPID) {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
		hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

		msg := errStyle.Render(fmt.Sprintf("Another wandersort process is already running (PID %d).", existingPID)) + "\n\n" +
			hintStyle.Render("Only one scan or server can use the same output directory at a time.") + "\n" +
			hintStyle.Render("Stop the other process first, or if it already exited, remove the lock file:") + "\n" +
			hintStyle.Render("  rm "+lockPath)

		return nil, fmt.Errorf("%s", msg)
	}

	// Remove stale lock file before attempting exclusive create
	os.Remove(lockPath)

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create lock file %s: %w", lockPath, err)
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

	return &Lock{path: lockPath}, nil
}

// Unlock removes the lock file. Safe to call multiple times.
func (l *Lock) Unlock() {
	if l == nil || l.path == "" {
		return
	}
	os.Remove(l.path)
	l.path = ""
}

func readLockPID(lockPath string) (int, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks existence without sending a real signal (Unix)
	err = process.Signal(syscall.Signal(0))
	return err == nil
}
