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

// leashTo leashes cmd, already started: a shell waits for a pipe only this
// process holds open, then kills it. The shell ignores the terminal's signals,
// which may end this process first.
func leashTo(cmd *exec.Cmd) (*leash, error) {
	watcher := exec.Command("/bin/sh", "-c", `trap '' HUP INT TERM; read -r _; kill -KILL "$1"`,
		"sh", strconv.Itoa(cmd.Process.Pid))
	if _, err := watcher.StdinPipe(); err != nil {
		return nil, fmt.Errorf("exiftool watcher pipe: %w", err)
	}
	if err := watcher.Start(); err != nil {
		return nil, fmt.Errorf("starting exiftool watcher: %w", err)
	}
	return &leash{watcher: watcher}, nil
}

// wait waits for cmd to exit. The watcher goes first: once cmd is reaped, its
// PID may be another process's.
func (l *leash) wait(cmd *exec.Cmd) error {
	l.watcher.Process.Kill()
	l.watcher.Wait()
	return cmd.Wait()
}
