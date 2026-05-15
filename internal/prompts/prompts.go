// Package prompts owns the //go:embed of system prompts and CONTEXT.csv.
//
// Embedding lives in this package (not at the binary's top level) because
// //go:embed cannot reach paths outside its own package directory.
// Consumers (internal/controls, internal/validator) read through the
// accessors below — never the embed FS directly.
package prompts

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed embedded/system_old.txt embedded/system_new.txt embedded/system_revised.txt embedded/CONTEXT.csv
var files embed.FS

// SystemMode selects which auditor system prompt to use. Names match the
// filenames embedded under embedded/system_<mode>.txt and the Python flag.
type SystemMode string

// Three system prompt modes ship in the binary. SystemRevised is the
// production default and matches the Python load_prompts default.
const (
	SystemOld     SystemMode = "old"
	SystemNew     SystemMode = "new"
	SystemRevised SystemMode = "revised"
)

// DefaultSystemMode mirrors the Python default in
// server/thor/thor_validator.py (load_prompts / validate_control).
const DefaultSystemMode = SystemRevised

// Valid reports whether m is a known system prompt mode.
func (m SystemMode) Valid() bool {
	switch m {
	case SystemOld, SystemNew, SystemRevised:
		return true
	default:
		return false
	}
}

// System returns the raw bytes of system_<mode>.txt.
func System(mode SystemMode) ([]byte, error) {
	if !mode.Valid() {
		return nil, fmt.Errorf("prompts: unknown system mode %q (want old, new, or revised)", mode)
	}
	name := fmt.Sprintf("embedded/system_%s.txt", mode)
	b, err := fs.ReadFile(files, name)
	if err != nil {
		return nil, fmt.Errorf("prompts: read %s: %w", name, err)
	}
	return b, nil
}

// ContextCSV returns the raw bytes of the embedded CONTEXT.csv.
func ContextCSV() ([]byte, error) {
	b, err := fs.ReadFile(files, "embedded/CONTEXT.csv")
	if err != nil {
		return nil, fmt.Errorf("prompts: read CONTEXT.csv: %w", err)
	}
	return b, nil
}
