package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Logger 是项目内部统一日志接口。
type Logger interface {
	Debug(ctx context.Context, msg string, attrs ...any)
	Info(ctx context.Context, msg string, attrs ...any)
	Error(ctx context.Context, msg string, attrs ...any)
}

type slogLogger struct {
	logger *slog.Logger
}

// NewDevelopment 创建输出到 stderr 的开发期 logger。
func NewDevelopment() Logger {
	return newSlogLogger(os.Stderr)
}

// NewDevelopmentFile 创建同时输出到 stderr 和文件的开发期 logger。
func NewDevelopmentFile(path string) (Logger, func() error, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}

	return newSlogLogger(io.MultiWriter(os.Stderr, file)), file.Close, nil
}

// NewNoop 创建丢弃所有日志的 logger。
func NewNoop() Logger {
	return noopLogger{}
}

func newSlogLogger(w io.Writer) Logger {
	return slogLogger{logger: slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))}
}

func (l slogLogger) Debug(ctx context.Context, msg string, attrs ...any) {
	l.logger.DebugContext(ctx, msg, attrs...)
}

func (l slogLogger) Info(ctx context.Context, msg string, attrs ...any) {
	l.logger.InfoContext(ctx, msg, attrs...)
}

func (l slogLogger) Error(ctx context.Context, msg string, attrs ...any) {
	l.logger.ErrorContext(ctx, msg, attrs...)
}

type noopLogger struct{}

func (noopLogger) Debug(ctx context.Context, msg string, attrs ...any) {}

func (noopLogger) Info(ctx context.Context, msg string, attrs ...any) {}

func (noopLogger) Error(ctx context.Context, msg string, attrs ...any) {}
