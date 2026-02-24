// https://gogoapps.io/blog/passing-loggers-in-go-golang-logging-best-practices/.
package logger

import (
	"log/slog"
	"os"

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
func New(logLvl string, console bool, logFile string, otel bool) Logger {
	if !console && logFile == "" && !otel {
		return NewNoopLogger()
	}

	level := getSlogLevel(logLvl)

	var handlers []slog.Handler

	if console {
		handlers = append(handlers, NewPrettyHandler(&slog.HandlerOptions{Level: level}))
	}

	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			f = os.Stderr
		}
		handlers = append(handlers, slog.NewJSONHandler(f, &slog.HandlerOptions{
			Level:     level,
			AddSource: true,
		}))
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

	return &ConsoleLogger{
		logger: slog.New(h),
		level:  level,
	}
}
