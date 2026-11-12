package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options controls the structured logger.
type Options struct {
	Level  string
	Format string
	Writer io.Writer
}

// New builds a slog logger with header redaction. The relay never logs request
// or response bodies, so no body redaction is needed here.
func New(opts Options) *slog.Logger {
	w := opts.Writer
	if w == nil {
		w = os.Stderr
	}
	level := parseLevel(opts.Level)
	handlerOpts := &slog.HandlerOptions{Level: level}
	var base slog.Handler
	if strings.EqualFold(opts.Format, "json") {
		base = slog.NewJSONHandler(w, handlerOpts)
	} else {
		base = slog.NewTextHandler(w, handlerOpts)
	}
	return slog.New(&redactHandler{next: base})
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "error":
		return slog.LevelError
	case "warn", "warning":
		return slog.LevelWarn
	case "debug":
		return slog.LevelDebug
	case "trace":
		return slog.LevelDebug - 4
	default:
		return slog.LevelInfo
	}
}

var sensitiveKeys = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"api-key":             true,
	"apikey":              true,
	"api_key":             true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"token":               true,
	"access_token":        true,
}

type redactHandler struct {
	next slog.Handler
}

func (h *redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a)
	}
	return &redactHandler{next: h.next.WithAttrs(redacted)}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{next: h.next.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if sensitiveKeys[strings.ToLower(a.Key)] {
		return slog.String(a.Key, "[REDACTED]")
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		out := make([]any, 0, len(group))
		for _, child := range group {
			out = append(out, redactAttr(child))
		}
		return slog.Group(a.Key, out...)
	}
	return a
}
