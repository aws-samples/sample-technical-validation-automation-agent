package validator

import (
	"os"
	"testing"
)

// Phase 1E.12 — parity test scaffold.
//
// PLAN.md gates the live parity diff (Go run vs Python run on the same
// partner folder) on Phase 3.5 dogfooding. That phase requires:
//   - the CLI (Phase 2) wired so we can drive a full convert→validate
//     pipeline end-to-end,
//   - real Bedrock credentials, and
//   - a partner folder pre-validated with Python whose output we can
//     diff against.
//
// Until then this test exists only to mark the gate. It runs only when
// the THOR_PARITY_FIXTURE env var points at a directory containing a
// previously-captured Python `validation_summary.md`. CI does not set
// the env var, so the test silently skips.
//
// When Phase 3.5 lands, this file gets the actual diff machinery: load
// the Python summary, run ValidateBatch through a live Bedrock client,
// and assert verdict-level equivalence on every control (reasoning text
// is allowed to drift due to LLM nondeterminism, as documented).
func TestParityAgainstPython_GatedByPhase35(t *testing.T) {
	fixture := os.Getenv("THOR_PARITY_FIXTURE")
	if fixture == "" {
		t.Skip("THOR_PARITY_FIXTURE unset; parity test deferred to Phase 3.5 dogfooding")
	}
	t.Skip("parity diff implementation deferred to Phase 3.5 (PLAN.md 1E.12)")
}
