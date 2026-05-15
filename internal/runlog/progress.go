package runlog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// ProgressFileName is the per-partner-folder progress log written
// alongside Bedrock calls (matches Python).
const ProgressFileName = "validation_progress.log"

// OpenProgressFile opens (or creates) <partnerFolder>/validation_progress.log
// in append mode. The returned closer must be called to flush. Caller
// owns the lifecycle.
func OpenProgressFile(partnerFolder string) (io.WriteCloser, error) {
	path := filepath.Join(partnerFolder, ProgressFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // path is operator-supplied
	if err != nil {
		return nil, fmt.Errorf("runlog: open %s: %w", path, err)
	}
	return f, nil
}

// NewProgressLogger builds a *slog.Logger that writes JSON records both
// to stderrLogger's underlying handler AND to the supplied progress file
// (typically opened with OpenProgressFile). The intent is that the
// operator sees a tail of progress on the terminal while the auditable
// log accumulates on disk.
//
// runID is attached as a default attribute so every progress line is
// linkable back to a specific run.
func NewProgressLogger(stderrW, fileW io.Writer, level Level, runID string) *slog.Logger {
	if level == 0 {
		level = LevelInfo
	}
	tee := io.MultiWriter(stderrW, fileW)
	return slog.New(slog.NewJSONHandler(tee, &slog.HandlerOptions{Level: level})).
		With(slog.String("run_id", runID))
}

// ControlEvent is the structured per-control log payload PLAN.md 1F.3
// pins. ControlAttempt is 1 for the first call (or the only call when
// consensus = 1) and increments through retries / consensus runs.
type ControlEvent struct {
	ControlID    string
	Attempt      int
	LatencyMS    int64
	InputTokens  int64
	OutputTokens int64
	Status       string // canonical Status.String() — PASSED/FAILED/WAIVED/ERRORED
}

// LogControlEvent emits e at LevelInfo on logger. Convenience over
// hand-rolling slog calls in every caller.
func LogControlEvent(logger *slog.Logger, e ControlEvent) {
	logger.Info("control_event",
		slog.String("control_id", e.ControlID),
		slog.Int("attempt", e.Attempt),
		slog.Int64("latency_ms", e.LatencyMS),
		slog.Int64("input_tokens", e.InputTokens),
		slog.Int64("output_tokens", e.OutputTokens),
		slog.String("status", e.Status),
	)
}

// Stopwatch is a tiny convenience: defer Stopwatch().Elapsed() to record
// a latency. Avoids importing time in every caller.
type Stopwatch struct{ start time.Time }

// Start returns a Stopwatch pinned to time.Now().
func Start() Stopwatch { return Stopwatch{start: time.Now()} }

// Elapsed returns the milliseconds since Start.
func (s Stopwatch) Elapsed() int64 { return time.Since(s.start).Milliseconds() }
