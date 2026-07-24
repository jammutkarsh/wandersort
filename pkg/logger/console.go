// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"
)

// ANSI color codes
const (
	ansiReset = "\033[0m"

	// ansiBlack        = 30
	// ansiRed          = 31
	// ansiGreen        = 32
	// ansiYellow       = 33
	// ansiBlue         = 34
	// ansiMagenta      = 35
	ansiCyan = 36
	// ansiLightGray    = 37
	ansiDarkGray = 90
	ansiLightRed = 91
	// ansiLightGreen   = 92
	ansiLightYellow = 93
	// ansiLightBlue    = 94
	ansiLightMagenta = 95
	// ansiLightCyan    = 96
	ansiWhite = 97
)

func colorize(colorCode int, v string) string {
	return fmt.Sprintf("\033[%dm%s%s", colorCode, v, ansiReset)
}

type SlogAdapter struct {
	logger *slog.Logger
	level  slog.Level
}

var _ Logger = &SlogAdapter{}

// log creates a record with the correct caller PC and dispatches it
func (l *SlogAdapter) log(level slog.Level, msg string, attrs ...any) {
	if !l.logger.Handler().Enabled(context.TODO(), level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(attrs...)
	_ = l.logger.Handler().Handle(context.TODO(), r)
}

func (l *SlogAdapter) Debug(msg string, attrs ...any) {
	l.log(slog.LevelDebug, msg, attrs...)
}

func (l *SlogAdapter) Info(msg string, attrs ...any) {
	l.log(slog.LevelInfo, msg, attrs...)
}

func (l *SlogAdapter) Warn(msg string, attrs ...any) {
	l.log(slog.LevelWarn, msg, attrs...)
}

func (l *SlogAdapter) Error(msg string, attrs ...any) {
	l.log(slog.LevelError, msg, attrs...)
}

func (l *SlogAdapter) Panic(msg string, attrs ...any) {
	l.log(slog.LevelError, msg, attrs...)
	panic(msg)
}

// UserKey marks a log call as user-facing. Set it truthy (e.g.
// log.Info("Scanning…", logger.UserKey, true)) and the message shows on the
// clean console; untagged Info/Debug lines go to the file log only. In debug
// mode the console shows everything.
const UserKey = "userFacing"

// sessionKey is the pipeline correlation id. It is printed once at session
// start and then omitted from every console line to avoid spamming it; the JSON
// file log still carries it on every record for traceability.
const sessionKey = "sessionId"

// PrettyHandler wraps a slog.JSONHandler, captures its output into a buffer,
// and re-formats it as a human-readable, coloured console line. It surfaces
// only user-facing lines (see UserKey) and warnings/errors, unless the
// configured level is debug, in which case it shows every record.
type PrettyHandler struct {
	handler  slog.Handler
	buf      *bytes.Buffer
	mu       *sync.Mutex
	minLevel slog.Level
}

// NewPrettyHandler creates a PrettyHandler with the given options
func NewPrettyHandler(opts *slog.HandlerOptions) *PrettyHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	minLevel := slog.LevelInfo
	if opts.Level != nil {
		minLevel = opts.Level.Level()
	}
	b := &bytes.Buffer{}
	return &PrettyHandler{
		buf: b,
		handler: slog.NewJSONHandler(b, &slog.HandlerOptions{
			Level:       opts.Level,
			AddSource:   false, // source is rendered manually as file:line
			ReplaceAttr: suppressDefaults(opts.ReplaceAttr),
		}),
		mu:       &sync.Mutex{},
		minLevel: minLevel,
	}
}

// suppressDefaults removes the default time/level/msg keys so that the
// PrettyHandler can render them itself while still forwarding any custom
// ReplaceAttr supplied by the caller
func suppressDefaults(
	next func([]string, slog.Attr) slog.Attr,
) func([]string, slog.Attr) slog.Attr {
	return func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey ||
			a.Key == slog.LevelKey ||
			a.Key == slog.MessageKey ||
			a.Key == slog.SourceKey {
			return slog.Attr{}
		}
		if next == nil {
			return a
		}
		return next(groups, a)
	}
}

func (h *PrettyHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *PrettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &PrettyHandler{handler: h.handler.WithAttrs(attrs), buf: h.buf, mu: h.mu, minLevel: h.minLevel}
}

func (h *PrettyHandler) WithGroup(name string) slog.Handler {
	return &PrettyHandler{handler: h.handler.WithGroup(name), buf: h.buf, mu: h.mu, minLevel: h.minLevel}
}

// computeAttrs delegates to the inner JSONHandler and unmarshals the result
func (h *PrettyHandler) computeAttrs(ctx context.Context, r slog.Record) (map[string]any, error) {
	h.mu.Lock()
	defer func() {
		h.buf.Reset()
		h.mu.Unlock()
	}()
	if err := h.handler.Handle(ctx, r); err != nil {
		return nil, fmt.Errorf("error when calling inner handler's Handle: %w", err)
	}
	var attrs map[string]any
	if err := json.Unmarshal(h.buf.Bytes(), &attrs); err != nil {
		return nil, fmt.Errorf("error when unmarshaling inner handler's Handle result: %w", err)
	}
	return attrs, nil
}

// Handle renders a clean, user-facing console line: a coloured level tag, the
// message, then any structured attributes as dimmed key=value pairs. Timestamp
// and source location are intentionally omitted here — the JSON file log keeps
// them for debugging (see report-issue).
func (h *PrettyHandler) Handle(ctx context.Context, r slog.Record) error {
	levelColor := ansiCyan
	switch r.Level {
	case slog.LevelDebug:
		levelColor = ansiDarkGray
	case slog.LevelInfo:
		levelColor = ansiCyan
	case slog.LevelWarn:
		levelColor = ansiLightYellow
	case slog.LevelError:
		levelColor = ansiLightRed
	}

	attrs, err := h.computeAttrs(ctx, r)
	if err != nil {
		return err
	}

	userFacing, _ := attrs[UserKey].(bool)
	delete(attrs, UserKey)
	// StreamKey lines are TUI-feed only; on the plain console they are neither
	// promoted nor rendered as a stray stream=true attr. They stay non-user-facing
	// so the level filter below still drops them unless in debug mode.
	delete(attrs, StreamKey)

	// In debug mode show everything; otherwise only user-facing lines and
	// warnings/errors reach the console — the rest is developer detail that
	// stays in the JSON file log.
	verbose := h.minLevel <= slog.LevelDebug
	if !verbose && !userFacing && r.Level < slog.LevelWarn {
		return nil
	}

	// The correlation id is printed once at session start; keep it out of every
	// other console line (it stays in the file log).
	if !verbose {
		delete(attrs, sessionKey)
		delete(attrs, PhaseKey)
		delete(attrs, EventKey)
		delete(attrs, ElapsedKey)
	}

	// Fixed-width level tag keeps message columns aligned.
	line := colorize(levelColor, fmt.Sprintf("%-5s", r.Level.String())) + " " + r.Message

	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		var valStr string
		switch val := attrs[k].(type) {
		case string:
			valStr = val
		case map[string]any:
			b, _ := json.Marshal(val)
			valStr = string(b)
		default:
			valStr = fmt.Sprintf("%v", val)
		}
		line += " " + colorize(ansiDarkGray, k+"="+valStr)
	}

	fmt.Fprintln(os.Stderr, line)
	return nil
}
