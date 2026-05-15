package version

import (
	"strings"
	"testing"
)

func TestStringIncludesVersion(t *testing.T) {
	got := String()
	if !strings.Contains(got, Version) {
		t.Errorf("String() = %q, want it to contain Version %q", got, Version)
	}
}

func TestShortReturnsVersion(t *testing.T) {
	if got := Short(); got != Version {
		t.Errorf("Short() = %q, want %q", got, Version)
	}
}
