package validator

import (
	"errors"
	"fmt"
	"strings"
)

// Status is the verdict the validator returns for a control. Audit fix B11
// pins this as a typed enum so downstream code never sees nil/None and
// audit fix B12 ensures the YES/NO/WAIVED prefix is parsed exactly once,
// here, by the validator.
type Status int

const (
	// StatusErrored indicates the call failed (Bedrock error, missing
	// evidence, etc.). Result.Err is non-nil.
	StatusErrored Status = iota
	// StatusPassed mirrors Python `result.startswith("YES")`.
	StatusPassed
	// StatusFailed mirrors Python `result.startswith("NO")` and the
	// implicit fail when no prefix matches.
	StatusFailed
	// StatusWaived is emitted when a control cannot be auto-decided and
	// requires manual review (B3: empty partner response on DOC-006).
	StatusWaived
)

// String returns the canonical textual form used in reports and logs.
// CLI/MCP renderers must consume this method rather than re-parsing
// Reasoning text.
func (s Status) String() string {
	switch s {
	case StatusPassed:
		return "PASSED"
	case StatusFailed:
		return "FAILED"
	case StatusWaived:
		return "WAIVED"
	case StatusErrored:
		return "ERRORED"
	default:
		return "UNKNOWN"
	}
}

// Verdict returns the YES/NO/WAIVED token (matching Python prefixes) or
// ERRORED for failed runs. Used in summary reports.
func (s Status) Verdict() string {
	switch s {
	case StatusPassed:
		return "YES"
	case StatusFailed:
		return "NO"
	case StatusWaived:
		return "WAIVED"
	case StatusErrored:
		return "ERRORED"
	default:
		return "UNKNOWN"
	}
}

// Result is the structured output of a single control validation. Carries
// status, the human-readable reason text emitted by Bedrock (with the YES/
// NO/WAIVED prefix already stripped), and optional error.
//
// Audit fixes:
//   - B11: error paths populate Err and set Status = StatusErrored.
//     Reasoning carries an actionable description; never empty.
//   - B12: prefix parsing happens once in parseResult. Renderers consume
//     the struct directly.
type Result struct {
	ControlID string
	Status    Status
	// Reasoning is Bedrock's free-form explanation with the leading
	// YES/NO/WAIVED prefix removed and surrounding whitespace trimmed.
	Reasoning string
	// Err is populated only when Status == StatusErrored.
	Err error
	// RawText is the unmodified Bedrock response. Useful for debug logs;
	// renderers should prefer Reasoning + Status.
	RawText string
}

// IsTerminal reports whether the result represents a non-erroring run.
// Convenience for ValidateBatch's "all-pass-twice" consensus early-stop.
func (r Result) IsTerminal() bool { return r.Status != StatusErrored }

// parseResult turns a raw Bedrock text response into a structured Result.
// Audit fix B12: this is the *only* place YES/NO/WAIVED prefixes are
// detected. Anything else in the codebase consumes the Status enum.
//
// Prefix matching is case-sensitive and anchored on the first non-space
// token, mirroring the Python `result.startswith("YES")` test.
func parseResult(controlID, raw string) Result {
	trimmed := strings.TrimLeft(raw, " \t\r\n")

	switch {
	case strings.HasPrefix(trimmed, "YES."):
		return Result{
			ControlID: controlID,
			Status:    StatusPassed,
			Reasoning: strings.TrimSpace(strings.TrimPrefix(trimmed, "YES.")),
			RawText:   raw,
		}
	case strings.HasPrefix(trimmed, "NO."):
		return Result{
			ControlID: controlID,
			Status:    StatusFailed,
			Reasoning: strings.TrimSpace(strings.TrimPrefix(trimmed, "NO.")),
			RawText:   raw,
		}
	case strings.HasPrefix(trimmed, "WAIVED."):
		return Result{
			ControlID: controlID,
			Status:    StatusWaived,
			Reasoning: strings.TrimSpace(strings.TrimPrefix(trimmed, "WAIVED.")),
			RawText:   raw,
		}
	}

	// No recognized prefix → treat as a fail with the original text as
	// reasoning. Mirrors the implicit Python fall-through (anything that
	// doesn't startswith("YES") fails).
	return Result{
		ControlID: controlID,
		Status:    StatusFailed,
		Reasoning: strings.TrimSpace(trimmed),
		RawText:   raw,
	}
}

// errResult constructs an ERRORED Result with an actionable reason.
// Reasoning is non-empty (B11). Wrapped err is preserved so callers can
// errors.Is / errors.As against it.
func errResult(controlID string, err error, format string, a ...any) Result {
	reason := fmt.Sprintf(format, a...)
	if err != nil {
		reason = fmt.Sprintf("%s: %v", reason, err)
	}
	return Result{
		ControlID: controlID,
		Status:    StatusErrored,
		Reasoning: reason,
		Err:       errors.Join(err, errors.New(reason)),
	}
}
