// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetSlogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"local", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"dev", slog.LevelWarn},
		{"warn", slog.LevelWarn},
		{"prod", slog.LevelError},
		{"error", slog.LevelError},
		{"garbage", slog.LevelDebug},
		{"", slog.LevelDebug},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := getSlogLevel(tt.input); got != tt.want {
				t.Errorf("getSlogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewReturnsNoopWithNoSinks(t *testing.T) {
	l := New("info", false, nil)
	if _, ok := l.(*nullLogger); !ok {
		t.Errorf("New(false, \"\") should return a no-op logger, got %T", l)
	}
}

func TestNewWithConsoleReturnsWorkingLogger(t *testing.T) {
	l := New("info", true, nil)
	if _, ok := l.(*SlogAdapter); !ok {
		t.Fatalf("New(true, \"\") should return a *SlogAdapter, got %T", l)
	}

	out := captureStderr(t, func() {
		l.Info("hello console", UserKey, true)
	})
	if !strings.Contains(out, "hello console") {
		t.Errorf("expected message on console, got %q", out)
	}
}

// readOnlyLog returns the contents of the single log file in dir.
func readOnlyLog(t *testing.T, dir string) string {
	t.Helper()
	logs := Recent(dir, 0)
	if len(logs) != 1 {
		t.Fatalf("want exactly one log file in %s, got %v", dir, logs)
	}
	data, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFileBuffersUntilPersist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	f := NewFile(dir)
	l := New("info", false, f)
	l.Debug("debug-only detail", "key", "value")

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("an unpersisted run must write nothing, not even the folder (err %v)", err)
	}
	if f.Path() != "" {
		t.Errorf("Path() = %q before Persist, want empty", f.Path())
	}

	f.Persist()
	f.Persist() // idempotent: still one file
	l.Info("after persist")

	if filepath.Dir(f.Path()) != dir || !strings.Contains(f.Path(), fmt.Sprint(os.Getpid())) {
		t.Errorf("Path() = %q, want a file in %s named with this PID", f.Path(), dir)
	}
	got := readOnlyLog(t, dir)
	for _, want := range []string{"debug-only detail", `"key":"value"`, `"source"`, "after persist"} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %s, got %q", want, got)
		}
	}
}

func TestFilePersistsOnWarning(t *testing.T) {
	dir := t.TempDir()
	l := New("info", false, NewFile(dir))
	l.Info("context before the problem")
	if len(Recent(dir, 0)) != 0 {
		t.Fatal("an info line must not persist the log")
	}
	l.Warn("something went wrong")
	got := readOnlyLog(t, dir)
	if !strings.Contains(got, "context before the problem") || !strings.Contains(got, "something went wrong") {
		t.Errorf("a warning must flush the buffered lines and itself, got %q", got)
	}
}

func TestFilePersistsPastBufferCap(t *testing.T) {
	dir := t.TempDir()
	f := NewFile(dir)
	if _, err := f.Write(make([]byte, maxBuffered+1)); err != nil {
		t.Fatal(err)
	}
	if f.Path() == "" {
		t.Error("a buffer past maxBuffered must persist")
	}
}

func TestFilePersistPrunes(t *testing.T) {
	dir := t.TempDir()
	for i := range keepLogs + 5 {
		name := fmt.Sprintf("2026-01-01T10-00-%02dZ_1.log", i)
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	f := NewFile(dir)
	f.Persist()

	got := Recent(dir, 0)
	if len(got) != keepLogs {
		t.Fatalf("after prune %d logs left, want %d", len(got), keepLogs)
	}
	if got[0] != f.Path() {
		t.Errorf("newest log = %s, want this run's %s", got[0], f.Path())
	}
	if want := fmt.Sprintf("2026-01-01T10-00-%02dZ_1.log", 6); filepath.Base(got[keepLogs-1]) != want {
		t.Errorf("oldest survivor = %s, want %s", filepath.Base(got[keepLogs-1]), want)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Errorf("non-log file must be left alone: %v", err)
	}
	if n := len(Recent(dir, 3)); n != 3 {
		t.Errorf("Recent(dir, 3) returned %d files", n)
	}
	if Recent(filepath.Join(dir, "missing"), 0) != nil {
		t.Error("Recent on a missing folder should be empty")
	}
}

func TestNewTUIRoutesToSinkAndFile(t *testing.T) {
	dir := t.TempDir()
	f := NewFile(dir)

	var events []Event
	sink := func(e Event) { events = append(events, e) }

	l := NewTUI("info", f, sink)
	l.Info("tui milestone", UserKey, true)
	l.Debug("hidden from sink at info level")
	f.Persist()

	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event forwarded to sink, got %d: %+v", len(events), events)
	}
	if events[0].Message != "tui milestone" {
		t.Errorf("sink event message = %q, want %q", events[0].Message, "tui milestone")
	}
	if !strings.Contains(readOnlyLog(t, dir), "hidden from sink at info level") {
		t.Errorf("file log should still capture the debug line NewTUI's sink filtered out")
	}
}
