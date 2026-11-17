package plugin

import (
	"context"
	"log/slog"

	"github.com/brudil/workspace-kit/protocol"
)

// logHandler writes the plugin's log to stderr (which ws keeps as the
// plugin's log file), and tells ws of warnings and errors, so the last
// one shows in ws plugin list.
type logHandler struct {
	p    *Plugin
	text slog.Handler
}

func (h *logHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.text.Enabled(ctx, l) }

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		level := "warn"
		if r.Level >= slog.LevelError {
			level = "error"
		}
		h.p.notify(protocol.MethodLog, protocol.LogParams{Level: level, Message: r.Message})
	}
	return h.text.Handle(ctx, r)
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &logHandler{p: h.p, text: h.text.WithAttrs(attrs)}
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	return &logHandler{p: h.p, text: h.text.WithGroup(name)}
}
