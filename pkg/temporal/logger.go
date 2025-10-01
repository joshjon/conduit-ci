package temporal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	tlog "go.temporal.io/sdk/log"

	"github.com/joshjon/conduit-ci/pkg/log"
)

type Level string

func (l Level) SlogLevel() slog.Level {
	switch l {
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		panic(fmt.Sprintf("unknown level: %v", l))
	}
}

const (
	LevelDebug Level = "DEBUG"
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
)

type Logger struct {
	logger        log.Logger
	globalKeyvals string
}

func FromLogger(base log.Logger) *Logger {
	return &Logger{
		logger: base,
	}
}

func (l *Logger) Debug(msg string, keyvals ...any) {
	l.log(LevelDebug, msg, keyvals...)
}

func (l *Logger) Info(msg string, keyvals ...any) {
	l.log(LevelInfo, msg, keyvals...)
}

func (l *Logger) Warn(msg string, keyvals ...any) {
	l.log(LevelWarn, msg, keyvals...)
}

func (l *Logger) Error(msg string, keyvals ...any) {
	l.log(LevelError, msg, keyvals...)
}

// With returns new logger the prepend every log entry with keyvals.
func (l *Logger) With(keyvals ...any) tlog.Logger {
	logger := &Logger{
		logger: l.logger,
	}
	if l.globalKeyvals != "" {
		logger.globalKeyvals = l.globalKeyvals + " "
	}
	logger.globalKeyvals += strings.TrimSuffix(fmt.Sprintln(keyvals...), "\n")
	return logger
}

func (l *Logger) log(level Level, msg string, keyvals ...any) {
	if l.globalKeyvals == "" {
		l.logger.Log(context.Background(), level.SlogLevel(), msg, keyvals...)
	} else {
		l.logger.Log(context.Background(), level.SlogLevel(), msg, keyvals...)
	}
}
