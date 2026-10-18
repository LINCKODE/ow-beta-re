// Package logging provides a compact, colored slog handler:
//
//	02:42:26 INFO  [LOGIN]: client connected remote=127.0.0.1:43048
//
// Colors are used when the output is a terminal and NO_COLOR is not set.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

const (
	reset   = "\033[0m"
	dim     = "\033[2m"
	bold    = "\033[1m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
)

// tagColors gives each server its own tag color, so interleaved output is easy to tell apart.
var tagColors = map[string]string{
	"LOGIN": cyan,
	"LOBBY": magenta,
	"LOGIC": green,
	"GAME":  yellow,
}

// New returns a logger that writes to stderr, tagged with name (e.g. "login").
func New(name string, level slog.Leveler) *slog.Logger {
	return slog.New(NewHandler(os.Stderr, name, level, colorEnabled(os.Stderr)))
}

type Handler struct {
	w     io.Writer
	mu    *sync.Mutex
	tag   string
	level slog.Leveler
	color bool
	attrs string // pre-formatted attributes from WithAttrs
	group string // key prefix from WithGroup
}

func NewHandler(w io.Writer, name string, level slog.Leveler, color bool) *Handler {
	if level == nil {
		level = slog.LevelInfo
	}
	return &Handler{w: w, mu: &sync.Mutex{}, tag: strings.ToUpper(name), level: level, color: color}
}

func (h *Handler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	if !r.Time.IsZero() {
		b.WriteString(h.paint(dim, r.Time.Format("15:04:05")))
		b.WriteByte(' ')
	}
	levelColor, levelName := levelStyle(r.Level)
	b.WriteString(h.paint(levelColor, fmt.Sprintf("%-5s", levelName)))
	tagColor, ok := tagColors[h.tag]
	if !ok {
		tagColor = magenta
	}
	b.WriteString(h.paint(bold+tagColor, " ["+h.tag+"]"))
	b.WriteString(": ")
	b.WriteString(r.Message)
	b.WriteString(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		h.appendAttr(&b, h.group, a)
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var b strings.Builder
	for _, a := range attrs {
		h.appendAttr(&b, h.group, a)
	}
	h2 := *h
	h2.attrs += b.String()
	return &h2
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h2 := *h
	h2.group += name + "."
	return &h2
}

func (h *Handler) appendAttr(b *strings.Builder, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, ga := range a.Value.Group() {
			h.appendAttr(b, prefix+a.Key+".", ga)
		}
		return
	}
	b.WriteByte(' ')
	b.WriteString(h.paint(dim, prefix+a.Key+"="))
	b.WriteString(quoteIfNeeded(a.Value.String()))
}

func (h *Handler) paint(color, s string) string {
	if !h.color {
		return s
	}
	return color + s + reset
}

func levelStyle(l slog.Level) (color, name string) {
	switch {
	case l >= slog.LevelError:
		return red, "ERROR"
	case l >= slog.LevelWarn:
		return yellow, "WARN"
	case l >= slog.LevelInfo:
		return green, "INFO"
	default:
		return blue, "DEBUG"
	}
}

func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if unicode.IsSpace(r) || r == '"' || r == '=' || !unicode.IsPrint(r) {
			return strconv.Quote(s)
		}
	}
	return s
}

func colorEnabled(f *os.File) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
