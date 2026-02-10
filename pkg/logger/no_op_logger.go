package logger

// nullLogger is a logger that does nothing, useful for testing
type nullLogger struct{}

// NewNoopLogger creates a new no-op logger
func NewNoopLogger() Logger {
	return &nullLogger{}
}

func (l *nullLogger) Debug(msg string, logItems ...any) {}

func (l *nullLogger) Info(msg string, logItems ...any) {}

func (l *nullLogger) Warn(msg string, logItems ...any) {}

func (l *nullLogger) Error(msg string, logItems ...any) {}

func (l *nullLogger) Fatal(msg string, logItems ...any) {}

func (l *nullLogger) Panic(msg string, logItems ...any) {}
