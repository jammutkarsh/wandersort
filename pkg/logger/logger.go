// https://gogoapps.io/blog/passing-loggers-in-go-golang-logging-best-practices/.
package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	slogmulti "github.com/samber/slog-multi"
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
//     (call after telemetry.Setup() has registered the provider)
//
// Passing console=false, logFile="", otel=false returns a no-op logger.
func New(logLvl string, console bool, logFile string) Logger {
	if !console && logFile == "" {
		return NewNoopLogger()
	}

	level := getSlogLevel(logLvl)

	var handlers []slog.Handler

	// Enable ConsoleLogger if provided
	if console {
		consoleH := NewPrettyHandler(&slog.HandlerOptions{Level: level})
		handlers = append(handlers, consoleH)
	}

	// Enable FileLogger if provided
	if logFile != "" {
		if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: failed to create log directory %s: %v\n", filepath.Dir(logFile), err)
		}

		file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: failed to open log file %s: %v (file logging disabled)\n", logFile, err)
		} else {
			jsonH := slog.NewJSONHandler(file, &slog.HandlerOptions{Level: level, AddSource: true})
			handlers = append(handlers, jsonH)
		}
	}

	return &SlogAdapter{
		logger: slog.New(slogmulti.Fanout(handlers...)),
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
