package logging

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

var sensitiveFragments = []string{
	"authorization",
	"cookie",
	"password",
	"secret",
	"token",
	"api_key",
	"apikey",
}

func NewJSON(output io.Writer, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if isSensitive(attr.Key) {
				return slog.String(attr.Key, "[REDACTED]")
			}
			return attr
		},
	})
	return slog.New(&redactingHandler{Handler: handler})
}

type redactingHandler struct {
	slog.Handler
}

func (handler *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for index, attr := range attrs {
		if isSensitive(attr.Key) {
			redacted[index] = slog.String(attr.Key, "[REDACTED]")
		} else {
			redacted[index] = attr
		}
	}
	return &redactingHandler{Handler: handler.Handler.WithAttrs(redacted)}
}

func (handler *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{Handler: handler.Handler.WithGroup(name)}
}

func (handler *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	return handler.Handler.Handle(ctx, record)
}

func isSensitive(key string) bool {
	normalized := strings.ToLower(key)
	for _, fragment := range sensitiveFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}
