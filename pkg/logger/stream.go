package logger

import (
	"context"
	"log/slog"
)

// StreamKey marks a high-volume per-item line (e.g. one per scanned file) for
// the TUI's live feed. Streams into the TUI + JSON log, hidden on the plain
// console — unlike UserKey milestones, which show everywhere.
const StreamKey = "stream"

// PhaseKey/EventKey tag a pipeline phase transition ("scan"/"metadata"/"vfs",
// "start"/"done") for TUI routing. Hidden on the plain console.
const (
	PhaseKey = "phase"
	EventKey = "event"
)

// ElapsedKey carries a phase's elapsed time on its "done" event; the TUI
// right-aligns it.
const ElapsedKey = "elapsed"

// Event is one log record delivered to a TUI Sink, with UserFacing and Stream
// lifted out of the attrs.
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

// teaHandler turns records into Events, forwarding only UserKey/StreamKey lines
// and warnings/errors (everything at debug level).
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
