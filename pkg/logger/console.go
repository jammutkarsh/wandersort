// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ANSI colour codes for the console line
const (
	ansiReset       = "\033[0m"
	ansiCyan        = 36
	ansiDarkGray    = 90
	ansiLightRed    = 91
	ansiLightYellow = 93
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

// UserKey marks a log call as user-facing. Set it truthy (e.g.
// log.Info("Scanning…", logger.UserKey, true)) and the message shows on the
// console; untagged Info/Debug lines go to the file log only.
const UserKey = "userFacing"

// sessionKey is the pipeline correlation id. Printed once at session start and
// then dropped from console lines; the JSON file log keeps it on every record.
const sessionKey = "sessionId"

// consoleHiddenKeys are the TUI-only routing attrs and the correlation id
// already printed at session start; the plain console hides them.
var consoleHiddenKeys = []string{sessionKey, PhaseKey, EventKey, ElapsedKey}

// PrettyHandler renders records as human-readable, coloured console lines. It
// surfaces only user-facing lines (see UserKey) and warnings/errors.
type PrettyHandler struct {
	minLevel slog.Level
}

// NewPrettyHandler creates a PrettyHandler with the given options
func NewPrettyHandler(opts *slog.HandlerOptions) *PrettyHandler {
	minLevel := slog.LevelInfo
	if opts != nil && opts.Level != nil {
		minLevel = opts.Level.Level()
	}
	return &PrettyHandler{minLevel: minLevel}
}

func (h *PrettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

// WithAttrs/WithGroup are no-ops: callers log with inline attrs, never
// logger.With, so there is nothing to carry.
func (h *PrettyHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *PrettyHandler) WithGroup(string) slog.Handler      { return h }

// Handle renders one console line: a coloured level tag, the message, then the
// remaining attrs as dimmed key=value pairs. Timestamp and source stay in the
// JSON file log (see report-issue).
func (h *PrettyHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any, r.NumAttrs())
	var userFacing bool
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case UserKey:
			userFacing, _ = a.Value.Any().(bool)
		case StreamKey:
			// TUI-feed only: never promoted, never shown as a stray stream=true
		default:
			attrs[a.Key] = a.Value.Any()
		}
		return true
	})

	// in debug mode show everything; otherwise developer detail stays in the
	// file log and the routing/correlation attrs stay off the line
	verbose := h.minLevel <= slog.LevelDebug
	if !verbose {
		if !userFacing && r.Level < slog.LevelWarn {
			return nil
		}
		for _, k := range consoleHiddenKeys {
			delete(attrs, k)
		}
	}

	levelColor := ansiCyan
	switch r.Level {
	case slog.LevelDebug:
		levelColor = ansiDarkGray
	case slog.LevelWarn:
		levelColor = ansiLightYellow
	case slog.LevelError:
		levelColor = ansiLightRed
	}

	// fixed-width level tag keeps message columns aligned
	var line strings.Builder
	line.WriteString(colorize(levelColor, fmt.Sprintf("%-5s", r.Level.String())))
	line.WriteString(" ")
	line.WriteString(r.Message)

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
		line.WriteString(" ")
		line.WriteString(colorize(ansiDarkGray, k+"="+valStr))
	}

	fmt.Fprintln(os.Stderr, line.String())
	return nil
}
