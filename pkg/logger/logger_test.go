// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import (
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
	l := New("info", false, "")
	if _, ok := l.(*nullLogger); !ok {
		t.Errorf("New(false, \"\") should return a no-op logger, got %T", l)
	}
}

func TestNewWithConsoleReturnsWorkingLogger(t *testing.T) {
	l := New("info", true, "")
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

func TestNewWithLogFileWritesJSON(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "nested", "wandersort.log")

	l := New("info", false, logFile)
	if _, ok := l.(*SlogAdapter); !ok {
		t.Fatalf("New(false, logFile) should return a *SlogAdapter, got %T", l)
	}
	l.Debug("debug-only detail", "key", "value")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected log file to exist and be readable: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "debug-only detail") {
		t.Errorf("file log missing message, got %q", got)
	}
	if !strings.Contains(got, `"key":"value"`) {
		t.Errorf("file log missing attrs, got %q", got)
	}
	if !strings.Contains(got, `"source"`) {
		t.Errorf("file log missing source (AddSource), got %q", got)
	}
}

func TestFileHandlerEmptyPathReturnsNil(t *testing.T) {
	if h := fileHandler(""); h != nil {
		t.Errorf("fileHandler(\"\") = %v, want nil", h)
	}
}

func TestFileHandlerCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "a", "b", "c.log")

	h := fileHandler(logFile)
	if h == nil {
		t.Fatal("fileHandler should succeed and create missing parent dirs")
	}
	if _, err := os.Stat(filepath.Dir(logFile)); err != nil {
		t.Errorf("expected parent dir to be created: %v", err)
	}
}

func TestNewTUIRoutesToSinkAndFile(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "tui.log")

	var events []Event
	sink := func(e Event) { events = append(events, e) }

	l := NewTUI("info", logFile, sink)
	l.Info("tui milestone", UserKey, true)
	l.Debug("hidden from sink at info level")

	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event forwarded to sink, got %d: %+v", len(events), events)
	}
	if events[0].Message != "tui milestone" {
		t.Errorf("sink event message = %q, want %q", events[0].Message, "tui milestone")
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected file log to capture everything: %v", err)
	}
	if !strings.Contains(string(data), "hidden from sink at info level") {
		t.Errorf("file log should still capture the debug line NewTUI's sink filtered out")
	}
}
