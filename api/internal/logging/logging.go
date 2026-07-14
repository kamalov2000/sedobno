// Package logging настраивает структурированный логгер slog (§6 SPEC:
// уровень из env).
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New создаёт JSON-логгер с уровнем из строки (debug|info|warn|error).
func New(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}
