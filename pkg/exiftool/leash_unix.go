//go:build !windows

package exiftool

import (
	"fmt"
	"os/exec"
	"strconv"
)

// leash ends one exiftool when this process dies without closing it: in
// -stay_open mode exiftool polls its input forever, end of input included.
type leash struct{ watcher *exec.Cmd }

// startLeashed starts cmd and a shell that waits for a pipe only this process
// holds open, then kills it. The shell ignores the terminal's signals, which
// may end this process first.
func startLeashed(cmd *exec.Cmd) (*leash, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	watcher, err := watch(cmd.Process.Pid)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}
	return &leash{watcher: watcher}, nil
}

func watch(pid int) (*exec.Cmd, error) {
	watcher := exec.Command("/bin/sh", "-c", `trap '' HUP INT TERM; read -r _; kill -KILL "$1"`,
		"sh", strconv.Itoa(pid))
	if _, err := watcher.StdinPipe(); err != nil {
		return nil, fmt.Errorf("exiftool watcher pipe: %w", err)
	}
	if err := watcher.Start(); err != nil {
		return nil, fmt.Errorf("starting exiftool watcher: %w", err)
	}
	return watcher, nil
}

// wait waits for cmd to exit. The watcher goes first: once cmd is reaped, its
// PID may be another process's.
func (l *leash) wait(cmd *exec.Cmd) error {
	l.watcher.Process.Kill()
	l.watcher.Wait()
	return cmd.Wait()
}
