// Package runlog configures structured (slog/JSON) logging and writes
// per-run manifests to <partner>/reports/run_<ts>.json.
//
// Hard rule: this package never writes to os.Stdout. The MCP transport
// is stdio-based and any non-protocol bytes on stdout corrupt the
// connection. All log output goes to stderr (default) or to a file
// handle the caller controls.
package runlog

import (
	"io"
	"log/slog"
	"os"
)

// Level is re-exported so callers don't need to import log/slog directly.
type Level = slog.Level

// Re-exported levels for convenience.
const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

// Options configures NewLogger.
type Options struct {
	// Level is the minimum log level. Zero value = LevelInfo.
	Level Level
	// Writer is the destination. nil → os.Stderr (the only sane default
	// — never stdout per the package contract).
	Writer io.Writer
	// AddSource attaches the calling source file/line to each record.
	// Off by default; useful in dev runs.
	AddSource bool
}

// NewLogger builds a *slog.Logger emitting JSON records to the configured
// writer. Use this for both CLI runs and the MCP server.
//
// Audit fix (PLAN.md 1F.1): JSON to stderr only. Callers should never
// supply os.Stdout — passing it explicitly is allowed for tests, but
// defaults guard against accidental MCP corruption.
func NewLogger(opts Options) *slog.Logger {
	w := opts.Writer
	if w == nil {
		w = os.Stderr
	}
	level := opts.Level
	if level == 0 {
		level = LevelInfo
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     level,
		AddSource: opts.AddSource,
	}))
}

// MultiWriter tees writes to all w's. Used by NewProgressTee to write
// validation_progress.log AND stderr at the same time.
func MultiWriter(w ...io.Writer) io.Writer { return io.MultiWriter(w...) }
