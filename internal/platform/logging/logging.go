// Package logging builds the one shared slog.Logger and makes it
// context-aware: attributes stored with WithAttrs (request_id, user_id)
// reach every record that a *Context method logs. On request paths, log
// with the *Context methods, never with plain Info/Error.
package logging

import (
	"context"
	"io"
	"log/slog"
	"slices"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
)

// New builds the application logger: text in development, JSON otherwise.
// This is the only constructor of a logger. Everything else receives it as
// a parameter.
func New(w io.Writer, cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	var handler slog.Handler
	if cfg.Development() {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}
	return slog.New(contextHandler{handler})
}

type attrsKey struct{}

// WithAttrs returns a context that adds attrs to every record that is
// logged with it. Attrs accumulate across calls.
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	existing, _ := ctx.Value(attrsKey{}).([]slog.Attr)
	// Copy on write: sibling requests can share the parent slice. Never
	// append into its spare capacity.
	merged := append(slices.Clip(existing), attrs...)
	return context.WithValue(ctx, attrsKey{}, merged)
}

// contextHandler adds the context's attributes to each record.
type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs, ok := ctx.Value(attrsKey{}).([]slog.Attr); ok {
		r.AddAttrs(attrs...)
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
