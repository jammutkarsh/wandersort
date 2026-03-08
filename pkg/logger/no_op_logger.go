package logger

type nullLogger struct{}

func NewNoopLogger() Logger {
	return &nullLogger{}
}

var _ Logger = &nullLogger{}

func (l *nullLogger) Debug(msg string, logItems ...any) {}

func (l *nullLogger) Info(msg string, logItems ...any) {}

func (l *nullLogger) Warn(msg string, logItems ...any) {}

func (l *nullLogger) Error(msg string, logItems ...any) {}

func (l *nullLogger) Panic(msg string, logItems ...any) {}
