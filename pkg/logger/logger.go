// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// https://gogoapps.io/blog/passing-loggers-in-go-golang-logging-best-practices/
package logger

import (
	"context"
	"log/slog"
	"strings"

	slogmulti "github.com/samber/slog-multi"
)

// Logger is the shared logging interface used throughout the application
type Logger interface {
	Debug(msg string, attrs ...any)
	Info(msg string, attrs ...any)
	Warn(msg string, attrs ...any)
	Error(msg string, attrs ...any)
}

// New constructs a Logger fanning out to the coloured stderr console (when
// console is set) and the JSON file log (when file is non-nil). With neither,
// it returns a no-op logger.
func New(logLevel string, console bool, file *File) Logger {
	if !console && file == nil {
		return NewNoopLogger()
	}

	level := getSlogLevel(logLevel)

	var handlers []slog.Handler

	// Enable ConsoleLogger if provided
	if console {
		consoleH := NewPrettyHandler(&slog.HandlerOptions{Level: level})
		handlers = append(handlers, consoleH)
	}

	// Enable FileLogger if provided
	if file != nil {
		handlers = append(handlers, fileHandler(file))
	}

	return &SlogAdapter{
		logger: slog.New(slogmulti.Fanout(handlers...)),
		level:  level,
	}
}

// NewTUI builds a Logger for full-screen mode: the console handler is off (its
// writes would corrupt the alt-screen) and records flow to sink for the TUI to
// render. The JSON file log still captures everything; pass the same File
// the startup logger got, so one process keeps one log.
func NewTUI(logLevel string, file *File, sink Sink) Logger {
	level := getSlogLevel(logLevel)
	handlers := []slog.Handler{&teaHandler{sink: sink, minLevel: level}}
	if file != nil {
		handlers = append(handlers, fileHandler(file))
	}
	return &SlogAdapter{
		logger: slog.New(slogmulti.Fanout(handlers...)),
		level:  level,
	}
}

// fileHandler writes the JSON file log (debug level, with source) into file,
// and persists file on the first warning: a run that went wrong is worth
// keeping even if it never opened a library. Shared by New and NewTUI.
func fileHandler(file *File) slog.Handler {
	return persistOnWarn{slog.NewJSONHandler(file, &slog.HandlerOptions{Level: slog.LevelDebug, AddSource: true}), file}
}

type persistOnWarn struct {
	slog.Handler
	file *File
}

func (h persistOnWarn) Handle(ctx context.Context, r slog.Record) error {
	err := h.Handler.Handle(ctx, r) // buffered first, so the warning itself is in the flush
	if r.Level >= slog.LevelWarn {
		h.file.Persist()
	}
	return err
}

func (h persistOnWarn) WithAttrs(attrs []slog.Attr) slog.Handler {
	return persistOnWarn{h.Handler.WithAttrs(attrs), h.file}
}

func (h persistOnWarn) WithGroup(name string) slog.Handler {
	return persistOnWarn{h.Handler.WithGroup(name), h.file}
}

// getSlogLevel maps a human-readable string to slog.Level
func getSlogLevel(levelStr string) slog.Level {
	switch strings.ToLower(levelStr) {
	case "local", "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "dev", "warn":
		return slog.LevelWarn
	case "prod", "error":
		return slog.LevelError
	default:
		return slog.LevelDebug
	}
}
