package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ANSI color codes
const (
	ansiReset = "\033[0m"

	ansiBlack        = 30
	ansiRed          = 31
	ansiGreen        = 32
	ansiYellow       = 33
	ansiBlue         = 34
	ansiMagenta      = 35
	ansiCyan         = 36
	ansiLightGray    = 37
	ansiDarkGray     = 90
	ansiLightRed     = 91
	ansiLightGreen   = 92
	ansiLightYellow  = 93
	ansiLightBlue    = 94
	ansiLightMagenta = 95
	ansiLightCyan    = 96
	ansiWhite        = 97
)

const prettyTimeFormat = "[15:04:05.000]"

func colorize(colorCode int, v string) string {
	return fmt.Sprintf("\033[%sm%s%s", strconv.Itoa(colorCode), v, ansiReset)
}

// ---------------------------------------------------------------------------
// PrettyHandler – a colorful slog.Handler for local development
// ---------------------------------------------------------------------------

// PrettyHandler wraps a slog.JSONHandler, captures its output into a buffer,
// and re-formats it as a human-readable, coloured log line.
type PrettyHandler struct {
	h slog.Handler
	b *bytes.Buffer
	m *sync.Mutex
}

// NewPrettyHandler creates a PrettyHandler with the given options.
func NewPrettyHandler(opts *slog.HandlerOptions) *PrettyHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	b := &bytes.Buffer{}
	return &PrettyHandler{
		b: b,
		h: slog.NewJSONHandler(b, &slog.HandlerOptions{
			Level:       opts.Level,
			AddSource:   false, // source is rendered manually as file:line
			ReplaceAttr: suppressDefaults(opts.ReplaceAttr),
		}),
		m: &sync.Mutex{},
	}
}

// suppressDefaults removes the default time/level/msg keys so that the
// PrettyHandler can render them itself while still forwarding any custom
// ReplaceAttr supplied by the caller.
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
	return h.h.Enabled(ctx, level)
}

func (h *PrettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &PrettyHandler{h: h.h.WithAttrs(attrs), b: h.b, m: h.m}
}

func (h *PrettyHandler) WithGroup(name string) slog.Handler {
	return &PrettyHandler{h: h.h.WithGroup(name), b: h.b, m: h.m}
}

// computeAttrs delegates to the inner JSONHandler and unmarshals the result.
func (h *PrettyHandler) computeAttrs(ctx context.Context, r slog.Record) (map[string]any, error) {
	h.m.Lock()
	defer func() {
		h.b.Reset()
		h.m.Unlock()
	}()
	if err := h.h.Handle(ctx, r); err != nil {
		return nil, fmt.Errorf("error when calling inner handler's Handle: %w", err)
	}
	var attrs map[string]any
	if err := json.Unmarshal(h.b.Bytes(), &attrs); err != nil {
		return nil, fmt.Errorf("error when unmarshaling inner handler's Handle result: %w", err)
	}
	return attrs, nil
}

func (h *PrettyHandler) Handle(ctx context.Context, r slog.Record) error {
	level := r.Level.String() + ":"

	switch r.Level {
	case slog.LevelDebug:
		level = colorize(ansiDarkGray, level)
	case slog.LevelInfo:
		level = colorize(ansiCyan, level)
	case slog.LevelWarn:
		level = colorize(ansiLightYellow, level)
	case slog.LevelError:
		level = colorize(ansiLightRed, level)
	}

	sep := colorize(ansiDarkGray, " | ")

	// Format source as file:line and pkg.funcName
	fileStr := ""
	funcStr := ""
	if r.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := frames.Next()
		file := f.File
		if idx := strings.LastIndexByte(file, '/'); idx >= 0 {
			file = file[idx+1:]
		}
		fileStr = fmt.Sprintf("%s:%d", file, f.Line)
		// f.Function is like "github.com/org/repo/pkg.FuncName" — take after last '/'
		fn := f.Function
		if idx := strings.LastIndexByte(fn, '/'); idx >= 0 {
			fn = fn[idx+1:]
		}
		funcStr = fn
	}

	attrs, err := h.computeAttrs(ctx, r)
	if err != nil {
		return err
	}

	// Render attrs inline: key: value
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}

	var attrParts []string
	for _, k := range keys {
		v := attrs[k]
		var valStr string
		switch val := v.(type) {
		case string:
			valStr = val
		case map[string]any:
			b, _ := json.Marshal(val)
			valStr = string(b)
		default:
			valStr = fmt.Sprintf("%v", val)
		}
		attrParts = append(attrParts,
			colorize(ansiWhite, k+":")+" "+colorize(ansiDarkGray, valStr),
		)
	}

	// [TS] | LEVEL: | Message | [K: V | K: V]    file:line funcName
	line := colorize(ansiWhite, r.Time.Format(prettyTimeFormat))
	line += sep + level
	line += sep + colorize(ansiWhite, r.Message)
	if len(attrParts) > 0 {
		line += sep + colorize(ansiLightMagenta, "[ ") + strings.Join(attrParts, colorize(ansiDarkGray, " | ")) + colorize(ansiLightMagenta, " ]")
	}
	if fileStr != "" {
		line += "\t | " + colorize(ansiDarkGray, fileStr+" "+funcStr)
	}

	fmt.Fprintln(os.Stderr, line)
	return nil
}

// ---------------------------------------------------------------------------
// ConsoleLogger – wraps PrettyHandler and satisfies the Logger interface
// ---------------------------------------------------------------------------

var _ Logger = (*ConsoleLogger)(nil)

// ConsoleLogger is a pretty, colourful logger intended for local development.
type ConsoleLogger struct {
	logger *slog.Logger
	level  slog.Level
}

// NewConsoleLogger creates a new ConsoleLogger. The env string controls the
// minimum log level using the same mapping as NewSlogLogger (e.g. "debug",
// "info", "warn", "error", or environment names like "local", "dev", "prod").
func NewConsoleLogger(env string) Logger {
	level := getConsoleSlogLevel(strings.ToLower(env))
	handler := NewPrettyHandler(&slog.HandlerOptions{
		Level:     level,
		AddSource: true,
	})
	return &ConsoleLogger{
		logger: slog.New(handler),
		level:  level,
	}
}

func getConsoleSlogLevel(s string) slog.Level {
	switch s {
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

// log creates a record with the correct caller PC and dispatches it.
func (l *ConsoleLogger) log(level slog.Level, msg string, attrs ...any) {
	if !l.logger.Handler().Enabled(context.TODO(), level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(attrs...)
	_ = l.logger.Handler().Handle(context.TODO(), r)
}

func (l *ConsoleLogger) Debug(msg string, attrs ...any) {
	l.log(slog.LevelDebug, msg, attrs...)
}

func (l *ConsoleLogger) Info(msg string, attrs ...any) {
	l.log(slog.LevelInfo, msg, attrs...)
}

func (l *ConsoleLogger) Warn(msg string, attrs ...any) {
	l.log(slog.LevelWarn, msg, attrs...)
}

func (l *ConsoleLogger) Error(msg string, attrs ...any) {
	l.log(slog.LevelError, msg, attrs...)
}

func (l *ConsoleLogger) Panic(msg string, attrs ...any) {
	l.log(slog.LevelError, msg, attrs...)
	panic(msg)
}
