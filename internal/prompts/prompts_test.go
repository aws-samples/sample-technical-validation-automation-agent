package prompts

import (
	"strings"
	"testing"
)

func TestSystemReturnsAllThreeModes(t *testing.T) {
	for _, mode := range []SystemMode{SystemOld, SystemNew, SystemRevised} {
		b, err := System(mode)
		if err != nil {
			t.Fatalf("System(%q): %v", mode, err)
		}
		if len(b) == 0 {
			t.Fatalf("System(%q): empty payload", mode)
		}
	}
}

func TestSystemRejectsUnknownMode(t *testing.T) {
	if _, err := System("bogus"); err == nil {
		t.Fatal("System(\"bogus\"): want error, got nil")
	}
}

func TestContextCSVHasExpectedHeader(t *testing.T) {
	b, err := ContextCSV()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "controlId,prompt_context") {
		t.Errorf("CONTEXT.csv: unexpected header line: %q", firstLine(b))
	}
}

func TestDefaultSystemModeIsRevised(t *testing.T) {
	if DefaultSystemMode != SystemRevised {
		t.Errorf("DefaultSystemMode = %q, want %q (matches Python)", DefaultSystemMode, SystemRevised)
	}
}

func firstLine(b []byte) string {
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
