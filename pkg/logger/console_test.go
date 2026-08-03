// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// captureStderr redirects os.Stderr for the duration of fn and returns
// whatever was written to it. PrettyHandler writes straight to os.Stderr, so
// this is the only way to observe its output without changing the handler.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}

func newRecord(level slog.Level, msg string, attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time.Now(), level, msg, 0)
	r.AddAttrs(attrs...)
	return r
}

func TestPrettyHandlerEnabled(t *testing.T) {
	h := NewPrettyHandler(&slog.HandlerOptions{Level: slog.LevelWarn})
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Info should not be enabled when minLevel is Warn")
	}
	if !h.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("Warn should be enabled when minLevel is Warn")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Error should be enabled when minLevel is Warn")
	}
}

func TestNewPrettyHandlerDefaultsToInfo(t *testing.T) {
	h := NewPrettyHandler(nil)
	if h.minLevel != slog.LevelInfo {
		t.Errorf("minLevel = %v, want Info", h.minLevel)
	}
}

func TestPrettyHandlerShowsOnlyUserFacingAndWarnings(t *testing.T) {
	h := NewPrettyHandler(&slog.HandlerOptions{Level: slog.LevelInfo})

	tests := []struct {
		name string
		rec  slog.Record
		want bool // whether the line should appear on console
	}{
		{"untagged info hidden", newRecord(slog.LevelInfo, "background detail"), false},
		{"user-facing info shown", newRecord(slog.LevelInfo, "Scanning…", slog.Bool(UserKey, true)), true},
		{"warn shown even untagged", newRecord(slog.LevelWarn, "low disk space"), true},
		{"error shown even untagged", newRecord(slog.LevelError, "boom"), true},
		{"untagged debug hidden", newRecord(slog.LevelDebug, "trace"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStderr(t, func() {
				if err := h.Handle(context.Background(), tt.rec); err != nil {
					t.Fatalf("Handle: %v", err)
				}
			})
			got := strings.Contains(out, tt.rec.Message)
			if got != tt.want {
				t.Errorf("message %q present = %v, want %v (output: %q)", tt.rec.Message, got, tt.want, out)
			}
		})
	}
}

func TestPrettyHandlerHidesRoutingKeysInNonVerboseMode(t *testing.T) {
	h := NewPrettyHandler(&slog.HandlerOptions{Level: slog.LevelInfo})
	rec := newRecord(slog.LevelWarn, "phase transition",
		slog.String(PhaseKey, "scan"),
		slog.String(EventKey, "start"),
		slog.String(ElapsedKey, "1s"),
		slog.String(sessionKey, "abc123"),
		slog.String("visible", "yes"),
	)

	out := captureStderr(t, func() {
		if err := h.Handle(context.Background(), rec); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	})

	for _, hidden := range []string{PhaseKey, EventKey, ElapsedKey, sessionKey} {
		if strings.Contains(out, hidden+"=") {
			t.Errorf("hidden key %q leaked into console output: %q", hidden, out)
		}
	}
	if !strings.Contains(out, "visible=yes") {
		t.Errorf("non-hidden attr missing from console output: %q", out)
	}
}

func TestPrettyHandlerVerboseModeShowsRoutingKeys(t *testing.T) {
	// minLevel <= Debug puts the handler in verbose mode, where nothing is
	// filtered and routing keys are not stripped.
	h := NewPrettyHandler(&slog.HandlerOptions{Level: slog.LevelDebug})
	rec := newRecord(slog.LevelInfo, "untagged but visible in debug",
		slog.String(PhaseKey, "hash"),
	)

	out := captureStderr(t, func() {
		if err := h.Handle(context.Background(), rec); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	})

	if !strings.Contains(out, rec.Message) {
		t.Errorf("verbose mode should show untagged info lines, got: %q", out)
	}
	if !strings.Contains(out, PhaseKey+"=hash") {
		t.Errorf("verbose mode should not strip routing keys, got: %q", out)
	}
}

func TestPrettyHandlerNeverShowsStreamKey(t *testing.T) {
	h := NewPrettyHandler(&slog.HandlerOptions{Level: slog.LevelDebug})
	rec := newRecord(slog.LevelInfo, "per-file line", slog.Bool(StreamKey, true))

	out := captureStderr(t, func() {
		if err := h.Handle(context.Background(), rec); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	})

	if strings.Contains(out, StreamKey+"=") {
		t.Errorf("stream=true leaked onto the console line: %q", out)
	}
}

func TestPrettyHandlerWithAttrsAndWithGroupAreNoops(t *testing.T) {
	h := NewPrettyHandler(nil)
	if h.WithAttrs([]slog.Attr{slog.String("a", "b")}) != h {
		t.Error("WithAttrs should return the same handler")
	}
	if h.WithGroup("g") != h {
		t.Error("WithGroup should return the same handler")
	}
}

func TestColorize(t *testing.T) {
	got := colorize(ansiCyan, "hello")
	if !strings.Contains(got, "hello") {
		t.Errorf("colorize should preserve the input text, got %q", got)
	}
	if !strings.HasSuffix(got, ansiReset) {
		t.Errorf("colorize should terminate with ansiReset, got %q", got)
	}
}
