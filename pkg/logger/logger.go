// https://gogoapps.io/blog/passing-loggers-in-go-golang-logging-best-practices/.
package logger

import (
	"log/slog"
	"os"

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

// Backend selects the logger implementation.
type Backend int

const (
	// BackendConsole outputs pretty, colourful logs to stderr.
	BackendConsole Backend = iota
	// BackendJSON appends structured JSON logs to a file.
	BackendJSON
	// BackendBoth writes pretty logs to the console AND JSON to a file.
	BackendBoth
	// BackendNoop discards all output (useful in tests).
	BackendNoop
)

// Config holds all options for constructing a Logger.
type Config struct {
	// Backend selects the logger implementation. Defaults to BackendConsole.
	Backend Backend
	// Level controls the minimum log level ("debug", "info", "warn", "error",
	// or environment aliases "local", "dev", "prod"). Defaults to "debug".
	Level string
	// LogFile is the path to the JSON log file used by BackendJSON and
	// BackendBoth. Defaults to DefaultLogFile ("log.json").
	LogFile string
}

// Option is a functional option that mutates a Config.
type Option func(*Config)

// WithBackend sets the logger backend.
func WithBackend(b Backend) Option {
	return func(c *Config) { c.Backend = b }
}

// WithLevel sets the minimum log level.
func WithLevel(level string) Option {
	return func(c *Config) { c.Level = level }
}

// WithLogFile sets the path of the JSON log file.
func WithLogFile(path string) Option {
	return func(c *Config) { c.LogFile = path }
}

// New constructs a Logger from the supplied options.
// If no options are provided it returns a console logger at debug level.
//
// Examples:
//
//	// Pretty console only (development)
//	l := logger.New()
//
//	// JSON file only (production)
//	l := logger.New(logger.WithBackend(logger.BackendJSON), logger.WithLevel("warn"))
//
//	// Console + JSON file (default log.json)
//	l := logger.New(logger.WithBackend(logger.BackendBoth), logger.WithLevel("info"))
func New(opts ...Option) Logger {
	cfg := &Config{
		Backend: BackendConsole,
		Level:   "debug",
	}
	for _, o := range opts {
		o(cfg)
	}

	logFile := cfg.LogFile
	if logFile == "" {
		logFile = DefaultLogFile
	}

	switch cfg.Backend {
	case BackendJSON:
		return NewSlogLogger(cfg.Level, logFile)

	case BackendBoth:
		level := getSlogLevel(cfg.Level)

		// Pretty handler → stderr
		prettyH := NewPrettyHandler(&slog.HandlerOptions{Level: level})

		// JSON handler → file
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			f = os.Stderr
		}
		jsonH := slog.NewJSONHandler(f, &slog.HandlerOptions{
			Level:     level,
			AddSource: true,
		})

		return &ConsoleLogger{
			logger: slog.New(slogmulti.Fanout(prettyH, jsonH)),
			level:  level,
		}

	case BackendNoop:
		return NewNoopLogger()

	default:
		return NewConsoleLogger(cfg.Level)
	}
}
