// https://gogoapps.io/blog/passing-loggers-in-go-golang-logging-best-practices/
package logger

import (
	"context"
	"log/slog"

	slogmulti "github.com/samber/slog-multi"
)

// Logger is the shared logging interface used throughout the application
type Logger interface {
	Debug(msg string, attrs ...any)
	Info(msg string, attrs ...any)
	Warn(msg string, attrs ...any)
	Error(msg string, attrs ...any)
}

// consoleLevel is the console's level; the file log is always at debug.
const consoleLevel = slog.LevelInfo

// New constructs a Logger fanning out to the coloured stderr console and, when
// file is non-nil, the JSON file log.
func New(file *File) Logger {
	handlers := []slog.Handler{NewPrettyHandler(&slog.HandlerOptions{Level: consoleLevel})}
	if file != nil {
		handlers = append(handlers, fileHandler(file))
	}
	return &SlogAdapter{
		logger: slog.New(slogmulti.Fanout(handlers...)),
		level:  consoleLevel,
	}
}

// NewTUI builds a Logger for full-screen mode: no console handler (it would
// corrupt the alt-screen); records go to sink and the file log. Pass the
// startup logger's File so one process keeps one log.
func NewTUI(file *File, sink Sink) Logger {
	handlers := []slog.Handler{&teaHandler{sink: sink, minLevel: consoleLevel}}
	if file != nil {
		handlers = append(handlers, fileHandler(file))
	}
	return &SlogAdapter{
		logger: slog.New(slogmulti.Fanout(handlers...)),
		level:  consoleLevel,
	}
}

// fileHandler writes the JSON file log (debug level, with source) and persists
// it on the first warning.
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
