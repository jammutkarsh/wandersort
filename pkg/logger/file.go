// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
)

const (
	// keepLogs is how many log files Persist leaves in the folder, the new one
	// included. Only runs that persisted count, so exploring the app never
	// evicts a scan's log.
	keepLogs = 20
	// maxBuffered is how much an unpersisted run holds in memory before it
	// persists anyway: a run logging that much is doing real work.
	maxBuffered = 1 << 20
)

// File is one process's log file. Records are held in memory until Persist,
// and a run that never persists leaves no file at all — most launches only
// look around the app, and a log of that is noise that costs a retention slot.
// The caller persists once the run touches user data (a library is opened);
// fileHandler persists on the first warning.
type File struct {
	mu     sync.Mutex
	dir    string
	buf    bytes.Buffer
	f      *os.File
	failed bool
}

// NewFile returns an unpersisted log file for this process, to live in dir.
func NewFile(dir string) *File { return &File{dir: dir} }

func (l *File) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.f != nil:
		return l.f.Write(p)
	case l.failed:
		return len(p), nil // file logging is off; never fail the caller's log call
	}
	l.buf.Write(p)
	if l.buf.Len() > maxBuffered {
		l.persist()
	}
	return len(p), nil
}

// Persist creates the file, named by UTC start time and PID so concurrent
// processes never share one and names sort oldest first, flushes what was
// buffered, and prunes the folder to the newest keepLogs. Idempotent and
// nil-safe.
func (l *File) Persist() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.persist()
}

func (l *File) persist() {
	if l.f != nil || l.failed {
		return
	}
	if old := Recent(l.dir, 0); len(old) >= keepLogs {
		for _, p := range old[keepLogs-1:] {
			_ = os.Remove(p) // best-effort: a file still open on Windows stays until a later run
		}
	}
	name := time.Now().UTC().Format("2006-01-02T15-04-05Z") + "_" + strconv.Itoa(os.Getpid()) + ".log"
	path := filepath.Join(l.dir, name)
	err := os.MkdirAll(l.dir, 0o755)
	var file *os.File
	if err == nil {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: failed to open log file %s: %v (file logging disabled)\n", path, err)
		l.failed = true
		l.buf.Reset()
		return
	}
	_, _ = file.Write(l.buf.Bytes())
	l.buf.Reset()
	l.f = file
}

// Path is the persisted file's path, or "" while the run is still buffered.
func (l *File) Path() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return ""
	}
	return l.f.Name()
}

// Recent returns up to n log files in dir, newest first; n <= 0 means all.
func Recent(dir string, n int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var logs []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".log" {
			logs = append(logs, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(logs)
	slices.Reverse(logs)
	if n > 0 && len(logs) > n {
		logs = logs[:n]
	}
	return logs
}
