package observability

import (
	"context"
	"log/slog"

	"github.com/irinery/middlewareAuth/internal/security"
)

type Logger struct {
	base *slog.Logger
}

func NewLogger(base *slog.Logger) *Logger {
	if base == nil {
		base = slog.Default()
	}
	return &Logger{base: base}
}

func (l *Logger) Info(ctx context.Context, msg string, attrs ...slog.Attr) {
	l.base.LogAttrs(ctx, slog.LevelInfo, security.Redact(msg), redactAttrs(attrs)...)
}

func (l *Logger) Warn(ctx context.Context, msg string, attrs ...slog.Attr) {
	l.base.LogAttrs(ctx, slog.LevelWarn, security.Redact(msg), redactAttrs(attrs)...)
}

func (l *Logger) Error(ctx context.Context, msg string, attrs ...slog.Attr) {
	l.base.LogAttrs(ctx, slog.LevelError, security.Redact(msg), redactAttrs(attrs)...)
}

func redactAttrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Value.Kind() == slog.KindString {
			attr.Value = slog.StringValue(security.Redact(attr.Value.String()))
		}
		out = append(out, attr)
	}
	return out
}
