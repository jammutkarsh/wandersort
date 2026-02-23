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
	// OTelBridge enables the slog → OpenTelemetry log bridge so that every
	// log record is also forwarded to the configured OTel LoggerProvider.
	OTelBridge bool
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

// WithOTelBridge enables (or disables) forwarding log records to the global
// OpenTelemetry LoggerProvider. Call this after telemetry.Setup() has
// registered the global provider.
func WithOTelBridge(enabled bool) Option {
	return func(c *Config) { c.OTelBridge = enabled }
}

// New constructs a Logger from the supplied options.
// If no options are provided it returns a console logger at debug level.
//
// Examples:
//
//	// Pretty console only (development)
//	l := logger.New()
//
//	// Console + JSON file + OTel log bridge
//	l := logger.New(
//		logger.WithBackend(logger.BackendBoth),
//		logger.WithLevel("info"),
//		logger.WithOTelBridge(true),
//	)
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

	level := getSlogLevel(cfg.Level)

	// Collect slog.Handler backends to fan out to.
	buildHandlers := func(includeJSON bool) []slog.Handler {
		var handlers []slog.Handler

		// Pretty console handler
		if cfg.Backend == BackendConsole || cfg.Backend == BackendBoth {
			handlers = append(handlers, NewPrettyHandler(&slog.HandlerOptions{Level: level}))
		}

		// JSON file handler
		if includeJSON {
			f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				f = os.Stderr
			}
			handlers = append(handlers, slog.NewJSONHandler(f, &slog.HandlerOptions{
				Level:     level,
				AddSource: true,
			}))
		}

		// OTel log bridge
		if cfg.OTelBridge {
			handlers = append(handlers, otelslog.NewHandler("wandersort"))
		}

		return handlers
	}

	switch cfg.Backend {
	case BackendJSON:
		if !cfg.OTelBridge {
			// Fast path: no fan-out needed
			return NewSlogLogger(cfg.Level, logFile)
		}
		handlers := buildHandlers(true)
		return &ConsoleLogger{
			logger: slog.New(slogmulti.Fanout(handlers...)),
			level:  level,
		}

	case BackendBoth:
		handlers := buildHandlers(true)
		return &ConsoleLogger{
			logger: slog.New(slogmulti.Fanout(handlers...)),
			level:  level,
		}

	case BackendNoop:
		return NewNoopLogger()

	default: // BackendConsole
		if !cfg.OTelBridge {
			return NewConsoleLogger(cfg.Level)
		}
		handlers := buildHandlers(false)
		return &ConsoleLogger{
			logger: slog.New(slogmulti.Fanout(handlers...)),
			level:  level,
		}
	}
}
