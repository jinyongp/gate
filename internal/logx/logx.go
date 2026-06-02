// Package logx builds gate's loggers on top of stdlib log/slog: a text handler
// (coloured on a TTY, plain logfmt otherwise) and the stdlib JSON handler for
// JSONL output. It also provides access logging and size-based rotation.
package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"gate/internal/ui/policy"
)

// Format selects the runtime log encoding.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// New returns a logger writing to w. format is "text" or "json" (JSONL).
func New(w io.Writer, format string, level slog.Level) *slog.Logger {
	if format == FormatJSON {
		return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
	}
	return slog.New(&textHandler{w: w, mu: &sync.Mutex{}, level: level, color: policy.ColorEnabled(w)})
}

// textHandler is a compact slog.Handler: "HH:MM:SS.mmm LEVEL msg key=value".
type textHandler struct {
	w     io.Writer
	mu    *sync.Mutex
	level slog.Level
	color bool
	attrs []slog.Attr
}

func (h *textHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *textHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Time.Format("15:04:05.000"))
	b.WriteByte(' ')
	b.WriteString(h.levelLabel(r.Level))
	b.WriteByte(' ')
	b.WriteString(r.Message)
	for _, a := range h.attrs {
		writeAttr(&b, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, a)
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *textHandler) WithAttrs(as []slog.Attr) slog.Handler {
	nh := *h
	nh.attrs = append(append([]slog.Attr{}, h.attrs...), as...)
	return &nh
}

func (h *textHandler) WithGroup(string) slog.Handler { return h }

func (h *textHandler) levelLabel(l slog.Level) string {
	label := l.String()
	if !h.color {
		return label
	}
	switch {
	case l >= slog.LevelError:
		return "\x1b[31m" + label + "\x1b[0m"
	case l >= slog.LevelWarn:
		return "\x1b[33m" + label + "\x1b[0m"
	case l >= slog.LevelInfo:
		return "\x1b[32m" + label + "\x1b[0m"
	default:
		return "\x1b[90m" + label + "\x1b[0m"
	}
}

func writeAttr(b *strings.Builder, a slog.Attr) {
	b.WriteByte(' ')
	b.WriteString(a.Key)
	b.WriteByte('=')
	fmt.Fprint(b, a.Value.Any())
}
