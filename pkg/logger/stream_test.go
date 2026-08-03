// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import (
	"context"
	"log/slog"
	"testing"
)

func TestTeaHandlerEnabled(t *testing.T) {
	h := &teaHandler{minLevel: slog.LevelWarn}
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Info should not be enabled when minLevel is Warn")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Error should be enabled when minLevel is Warn")
	}
}

func TestTeaHandlerForwardsUserFacingStreamAndWarnings(t *testing.T) {
	tests := []struct {
		name string
		rec  slog.Record
		want bool
	}{
		{"untagged info dropped", newRecord(slog.LevelInfo, "quiet"), false},
		{"user-facing info forwarded", newRecord(slog.LevelInfo, "milestone", slog.Bool(UserKey, true)), true},
		{"stream info forwarded", newRecord(slog.LevelInfo, "per-file", slog.Bool(StreamKey, true)), true},
		{"warn forwarded even untagged", newRecord(slog.LevelWarn, "uh oh"), true},
		{"error forwarded even untagged", newRecord(slog.LevelError, "boom"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &teaHandler{minLevel: slog.LevelInfo}
			var got *Event
			h.sink = func(e Event) { got = &e }

			if err := h.Handle(context.Background(), tt.rec); err != nil {
				t.Fatalf("Handle: %v", err)
			}

			if (got != nil) != tt.want {
				t.Errorf("event forwarded = %v, want %v", got != nil, tt.want)
			}
		})
	}
}

func TestTeaHandlerVerboseModeForwardsEverything(t *testing.T) {
	h := &teaHandler{minLevel: slog.LevelDebug}
	var got *Event
	h.sink = func(e Event) { got = &e }

	rec := newRecord(slog.LevelDebug, "trace line")
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got == nil {
		t.Fatal("verbose mode should forward untagged debug lines")
	}
}

func TestTeaHandlerEventShape(t *testing.T) {
	h := &teaHandler{minLevel: slog.LevelInfo}
	var got Event
	h.sink = func(e Event) { got = e }

	rec := newRecord(slog.LevelWarn, "disk low",
		slog.Bool(UserKey, true),
		slog.Bool(StreamKey, true),
		slog.String(PhaseKey, "scan"),
	)
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got.Level != slog.LevelWarn {
		t.Errorf("Level = %v, want Warn", got.Level)
	}
	if got.Message != "disk low" {
		t.Errorf("Message = %q, want %q", got.Message, "disk low")
	}
	if !got.UserFacing {
		t.Error("UserFacing should be true")
	}
	if !got.Stream {
		t.Error("Stream should be true")
	}
	if got.Attrs[UserKey] != nil || got.Attrs[StreamKey] != nil {
		t.Errorf("UserKey/StreamKey should be lifted out of Attrs, got %v", got.Attrs)
	}
	if got.Attrs[PhaseKey] != "scan" {
		t.Errorf("Attrs[%s] = %v, want %q", PhaseKey, got.Attrs[PhaseKey], "scan")
	}
}

func TestTeaHandlerWithAttrsAndWithGroupAreNoops(t *testing.T) {
	h := &teaHandler{minLevel: slog.LevelInfo}
	if h.WithAttrs([]slog.Attr{slog.String("a", "b")}) != h {
		t.Error("WithAttrs should return the same handler")
	}
	if h.WithGroup("g") != h {
		t.Error("WithGroup should return the same handler")
	}
}
