// Package version exposes build metadata stamped in via -ldflags.
//
// Both `thor` and `thor-mcp` read these values to surface a consistent
// version string to users (CLI --version) and clients (MCP serverInfo).
package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the human-readable release version (e.g. "v0.1.0").
	Version = "dev"

	// Commit is the short git SHA the binary was built from.
	Commit = "unknown"

	// BuildDate is the UTC build timestamp (RFC3339).
	BuildDate = "unknown"
)

// String returns the canonical "thor <version> (<commit>) built <date> with <go>" line.
func String() string {
	return fmt.Sprintf("thor %s (%s) built %s with %s",
		Version, Commit, BuildDate, runtime.Version())
}

// Short returns just the version string (no surrounding metadata).
func Short() string {
	return Version
}
