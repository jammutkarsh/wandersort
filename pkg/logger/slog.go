package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

const DefaultLogFile = "log.json"

var _ Logger = (*SlogLogger)(nil)

// SlogLogger holds an instance of slog.Logger for structured logging
type SlogLogger struct {
	console *slog.Logger
	otel    *slog.Logger
	level   slog.Level
	hasOtel bool
	file    *os.File // non-nil when writing to a file; nil when writing to stderr
}

// Pool for stack trace buffers to reduce allocations
var pcPool = sync.Pool{
	New: func() any {
		return make([]uintptr, 1)
	},
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

// NewSlogLogger creates a new SlogLogger that appends JSON to the given file
// path (created if absent). Pass DefaultLogFile ("log.json") for the default.
func NewSlogLogger(env string, filePath string) Logger {
	logLevel := strings.ToLower(env)

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		// Fall back to stderr if the file cannot be opened.
		f = os.Stderr
	}

	options := &slog.HandlerOptions{
		Level:     getSlogLevel(logLevel),
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				if src, ok := a.Value.Any().(*slog.Source); ok {
					return slog.String(slog.SourceKey, fmt.Sprintf("%s:%d", src.File, src.Line))
				}
			}
			return a
		},
	}

	var fileField *os.File
	if f != os.Stderr {
		fileField = f
	}

	return &SlogLogger{
		console: slog.New(slog.NewJSONHandler(f, options)),
		level:   getSlogLevel(logLevel),
		file:    fileField,
	}
}

func AddOtelInSlog(sLog *SlogLogger /* Pass by reference to modify the original logger */, otelHandler slog.Handler) Logger {
	sLog.otel = slog.New(otelHandler)
	sLog.hasOtel = true
	return sLog
}

// Optimized log method - only collect stack trace when needed, with pooled buffers
func (l *SlogLogger) log(level slog.Level, msg string, attrs ...any) {
	if !l.console.Handler().Enabled(context.TODO(), level) {
		return
	}

	// Get a pointer to a slice from the pool
	pcsPtr := pcPool.Get().([]uintptr)
	// Defer putting the pointer back (this is allocation-free)
	defer pcPool.Put(pcsPtr)

	// Dereference the pointer to get the actual slice
	pcs := pcsPtr

	// --- Safety Check ---
	// Check the number of frames written to prevent panic on pcs[0]
	n := runtime.Callers(3, pcs)
	var pc uintptr
	if n > 0 {
		pc = pcs[0] // Safely get the first program counter
	}

	r := slog.NewRecord(time.Now(), level, msg, pc)
	r.Add(attrs...)
	_ = l.console.Handler().Handle(context.TODO(), r)

	if l.hasOtel {
		_ = l.otel.Handler().Handle(context.TODO(), r)
	}
}

// Usage: l.Debug("Debug message", "key1", value1, "key2", value2)
func (l *SlogLogger) Debug(msg string, attrs ...any) {
	l.log(slog.LevelDebug, msg, attrs...)
}

// Usage: l.Info("Info message", "key1", value1, "key2", value2)
func (l *SlogLogger) Info(msg string, attrs ...any) {
	l.log(slog.LevelInfo, msg, attrs...)
}

// Usage: l.Warn("Warning message", "key1", value1, "key2", value2)
func (l *SlogLogger) Warn(msg string, attrs ...any) {
	l.log(slog.LevelWarn, msg, attrs...)
}

// Usage: l.Error("Error message", "key1", value1, "key2", value2)
func (l *SlogLogger) Error(msg string, attrs ...any) {
	l.log(slog.LevelError, msg, attrs...)
}

// Usage: l.Panic("Panic message", "key1", value1, "key2", value2)
func (l *SlogLogger) Panic(msg string, attrs ...any) {
	l.log(slog.LevelError, msg, attrs...)
	panic(msg)
}
