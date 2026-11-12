// Package logging builds the slog logger every program uses. Never log tokens,
// passwords or the Authorization header; callers simply do not pass them.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New writes text logs to stderr with a "component" attribute.
func New(component, level string) *slog.Logger { return NewWriter(os.Stderr, component, level) }

// NewWriter is New with an explicit destination (the TUI passes a file).
func NewWriter(w io.Writer, component, level string) *slog.Logger {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lv})).With("component", component)
}
