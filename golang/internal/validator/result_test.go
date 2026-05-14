package validator

import (
	"errors"
	"strings"
	"testing"
)

func TestParseResult_RecognizesYESPrefix(t *testing.T) {
	r := parseResult("ACCT-001", "YES. partner satisfies the requirement")
	if r.Status != StatusPassed {
		t.Fatalf("status = %v, want StatusPassed", r.Status)
	}
	if r.Reasoning != "partner satisfies the requirement" {
		t.Errorf("reasoning = %q", r.Reasoning)
	}
}

func TestParseResult_RecognizesNOPrefix(t *testing.T) {
	r := parseResult("ACCT-001", "NO. evidence missing")
	if r.Status != StatusFailed {
		t.Fatalf("status = %v, want StatusFailed", r.Status)
	}
}

func TestParseResult_RecognizesWAIVEDPrefix(t *testing.T) {
	r := parseResult("ACCT-001", "WAIVED. manual review required")
	if r.Status != StatusWaived {
		t.Fatalf("status = %v", r.Status)
	}
	if r.Reasoning != "manual review required" {
		t.Errorf("reasoning = %q", r.Reasoning)
	}
}

// B12: parsing happens once. Renderers consume Status/Reasoning, never
// re-parse. Verify the trimmed prefix is gone from Reasoning.
func TestParseResult_StripsPrefix_B12(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"YES. ok", "ok"},
		{"NO. nope", "nope"},
		{"WAIVED.   needs review", "needs review"},
		{"  YES. leading whitespace", "leading whitespace"},
	} {
		r := parseResult("X", tc.in)
		if strings.HasPrefix(r.Reasoning, "YES.") || strings.HasPrefix(r.Reasoning, "NO.") || strings.HasPrefix(r.Reasoning, "WAIVED.") {
			t.Errorf("input %q: reasoning still has prefix: %q", tc.in, r.Reasoning)
		}
		if r.Reasoning != tc.want {
			t.Errorf("input %q: reasoning = %q, want %q", tc.in, r.Reasoning, tc.want)
		}
	}
}

func TestParseResult_NoPrefixFallsThroughToFAIL(t *testing.T) {
	r := parseResult("X", "some unstructured response")
	if r.Status != StatusFailed {
		t.Errorf("status = %v, want StatusFailed (Python parity)", r.Status)
	}
}

// B11: error path produces non-nil Reasoning AND wraps the err.
func TestErrResult_NeverNilReasoning_B11(t *testing.T) {
	r := errResult("X", errors.New("bedrock down"), "calling converse")
	if r.Status != StatusErrored {
		t.Errorf("status = %v, want StatusErrored", r.Status)
	}
	if r.Reasoning == "" {
		t.Error("reasoning must not be empty for ERRORED")
	}
	if r.Err == nil {
		t.Error("Err must be populated for ERRORED")
	}
	if !strings.Contains(r.Err.Error(), "bedrock down") {
		t.Errorf("wrapped err lost original: %v", r.Err)
	}
}

func TestStatus_StringRoundTrip(t *testing.T) {
	cases := []struct {
		s    Status
		want string
	}{
		{StatusPassed, "PASSED"},
		{StatusFailed, "FAILED"},
		{StatusWaived, "WAIVED"},
		{StatusErrored, "ERRORED"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("Status(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}
