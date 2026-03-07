// https://gogoapps.io/blog/passing-loggers-in-go-golang-logging-best-practices/.
package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	slogmulti "github.com/samber/slog-multi"
	"go.opentelemetry.io/contrib/bridges/otelslog"
)

// Logger is the shared logging interface used throughout the application.
type Logger interface {
	Debug(msg string, attrs ...any)
	Info(msg string, attrs ...any)
	Warn(msg string, attrs ...any)
	Error(msg string, attrs ...any)
	Panic(msg string, attrs ...any)
}

// New constructs a Logger.
//
//   - logLvl:  minimum log level ("debug", "info", "warn", "error")
//   - console: emit colourful logs to stderr
//   - logFile: path of the JSON log file; empty string disables file logging
//   - otel:    forward log records to the global OTel LoggerProvider
//     (call after telemetry.Setup() has registered the provider)
//
// Passing console=false, logFile="", otel=false returns a no-op logger.
func New(logLvl string, otel, console bool, logFile string) Logger {
	if !console && logFile == "" && !otel {
		return NewNoopLogger()
	}

	level := getSlogLevel(logLvl)

	var handlers []slog.Handler

	if console {
		handlers = append(handlers, NewPrettyHandler(&slog.HandlerOptions{Level: level}))
	}

	if logFile != "" {
		// Ensure the log file's parent directory exists.
		if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "warn: failed to create log directory %s: %v\n", filepath.Dir(logFile), err)
		}
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			// Skip the JSON file handler entirely instead of falling back to
			// stderr, which would interleave JSON with the pretty console output.
			fmt.Fprintf(os.Stderr, "warn: failed to open log file %s: %v (file logging disabled)\n", logFile, err)
		} else {
			handlers = append(handlers, slog.NewJSONHandler(f, &slog.HandlerOptions{
				Level:     level,
				AddSource: true,
			}))
		}
	}

	if otel {
		handlers = append(handlers, otelslog.NewHandler("wandersort"))
	}

	var h slog.Handler
	if len(handlers) == 1 {
		h = handlers[0]
	} else {
		h = slogmulti.Fanout(handlers...)
	}

	return &SlogAdapter{
		logger: slog.New(h),
		level:  level,
	}
}

func getSlogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
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
