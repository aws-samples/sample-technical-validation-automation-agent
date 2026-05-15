package runlog

import (
	"crypto/rand"
	"fmt"
)

// NewRunID returns a fresh RFC 4122 v4 UUID string. crypto/rand is used so
// no third-party UUID library is required (PLAN.md 1F.4).
func NewRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read does not return errors on any supported OS;
		// if it ever does, fail loudly rather than silently produce a
		// duplicate or all-zeros ID.
		panic(fmt.Errorf("runlog: crypto/rand.Read: %w", err))
	}
	// Set version (4) and variant (RFC 4122) bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
