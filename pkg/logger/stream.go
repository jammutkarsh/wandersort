// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import (
	"context"
	"log/slog"
)

// StreamKey marks a high-volume, per-item log line meant to keep a TUI user
// engaged — e.g. the scanner tagging each file it walks. Set it truthy
// (log.Debug("scanning", logger.StreamKey, true, "file", path)) and the line
// streams into the TUI's live feed and the JSON file log, but is kept OUT of
// the plain console (unlike UserKey milestones, which show everywhere).
const StreamKey = "stream"

// PhaseKey / EventKey tag a pipeline phase transition so a TUI can route it to
// the right stage row without matching on message prose. PhaseKey is the phase
// name ("scan"/"hash"/"score"/"vfs"); EventKey is "start" or "done". Both are
// stripped from the plain console (like sessionId) — they exist for the TUI.
const (
	PhaseKey = "phase"
	EventKey = "event"
)

// ElapsedKey carries a phase's own elapsed-time measurement on its "done"
// event, separate from the message text (which embeds it for the plain
// console). The TUI right-aligns it Docker-style and strips the duplicate
// from the message. Hidden on the plain console like PhaseKey/EventKey.
const ElapsedKey = "elapsed"

// Event is one log record delivered to a TUI Sink. UserFacing and Stream are
// lifted out of the attrs (from UserKey/StreamKey) so the TUI can route the
// record without re-inspecting them; Attrs holds everything else.
type Event struct {
	Level      slog.Level
	Message    string
	Attrs      map[string]any
	UserFacing bool
	Stream     bool
}

// Sink receives Events on the logging goroutine. Implementations must not block
// for long (forward to a buffered channel / tea.Program.Send).
type Sink func(Event)

// teaHandler is the third fan-out handler used in TUI mode: it turns each
// record into an Event and hands it to the Sink. It forwards only the lines a
// user should see — UserKey milestones, StreamKey feed lines, and warnings/
// errors — unless the level is debug, in which case it forwards everything.
type teaHandler struct {
	sink     Sink
	minLevel slog.Level
}

func (h *teaHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

func (h *teaHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any, r.NumAttrs())
	var userFacing, stream bool
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case UserKey:
			userFacing, _ = a.Value.Any().(bool)
		case StreamKey:
			stream, _ = a.Value.Any().(bool)
		default:
			attrs[a.Key] = a.Value.Any()
		}
		return true
	})

	verbose := h.minLevel <= slog.LevelDebug
	if !verbose && !userFacing && !stream && r.Level < slog.LevelWarn {
		return nil
	}
	h.sink(Event{
		Level:      r.Level,
		Message:    r.Message,
		Attrs:      attrs,
		UserFacing: userFacing,
		Stream:     stream,
	})
	return nil
}

// WithAttrs/WithGroup are no-ops: the pipeline logs with inline attrs, never
// logger.With, so there is nothing to carry.
func (h *teaHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *teaHandler) WithGroup(string) slog.Handler      { return h }
