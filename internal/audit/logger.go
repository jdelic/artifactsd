// Package audit emits artifactsd's three log streams (design/01 §7, design/06 §7):
//
//   - Application logs and errors -> stderr, via log/slog, level-controlled.
//   - Access logs -> stdout, one structured event per request (decision F5).
//   - Audit events (admin actions, deletions) -> persisted in the DB and
//     mirrored to stderr.
//
// There are no log files and no DB-backed access log; aggregation is the
// deployer's job (containers/journald capture stdout/stderr).
package audit

import (
	"io"
	"log/slog"
	"strings"
)

// ParseLevel maps a textual log level to slog.Level, defaulting to Info for
// unknown values.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewAppLogger builds the application logger that writes to stderr (w) at the
// given level. It uses slog's text handler for human-friendly local output;
// deployers who want JSON can post-process or we can make this configurable
// later.
func NewAppLogger(w io.Writer, level string) *slog.Logger {
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: ParseLevel(level)})
	return slog.New(h)
}
