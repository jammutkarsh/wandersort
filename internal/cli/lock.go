package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"github.com/jammutkarsh/wandersort/pkg/utils"
)

const (
	lockFileName        = ".wandersort.lock"
	installLockFileName = ".wandersort-install.lock"
)

// acquireOutputLock takes the exclusive output-dir lock used by scan and serve.
// It renders a helpful message when another live wandersort process holds it.
func acquireOutputLock(dir string) (*utils.Lock, error) {
	lockPath := filepath.Join(dir, lockFileName)
	lock, err := utils.Acquire(context.Background(), dir, lockFileName, false)
	if errors.Is(err, utils.ErrLockHeld) {
		pid, _ := utils.ReadLockPID(lockPath)
		return nil, alreadyRunningError(lockPath, pid)
	}
	return lock, err
}

// alreadyRunningError renders the user-facing message for a held output-dir lock.
func alreadyRunningError(lockPath string, pid int) error {
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	msg := errStyle.Render(fmt.Sprintf("Another wandersort process is already running (PID %d).", pid)) + "\n\n" +
		hintStyle.Render("Only one scan or server can use the same output directory at a time.") + "\n" +
		hintStyle.Render("Stop the other process first, or if it already exited, remove the lock file:") + "\n" +
		hintStyle.Render("  rm "+lockPath)

	return fmt.Errorf("%s", msg)
}
