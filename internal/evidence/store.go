package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// SaveMap writes m to <partnerFolder>/evidence_map.json with 0o600
// permissions. Returns the absolute path written.
func SaveMap(partnerFolder string, m *Map) (string, error) {
	if m == nil {
		return "", fmt.Errorf("evidence: SaveMap called with nil map")
	}
	path := filepath.Join(partnerFolder, MapFileName)
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("evidence: marshal map: %w", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", fmt.Errorf("evidence: write %s: %w", path, err)
	}
	return path, nil
}

// LoadMap reads the persisted evidence map from
// <partnerFolder>/evidence_map.json. Returns (nil, nil) when the file
// doesn't exist — callers should treat absence as "no map" rather than
// an error.
func LoadMap(partnerFolder string) (*Map, error) {
	path := filepath.Join(partnerFolder, MapFileName)
	body, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied folder
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("evidence: read %s: %w", path, err)
	}
	var m Map
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("evidence: parse %s: %w", path, err)
	}
	return &m, nil
}

// IsStale reports whether the supplied map's PartnerFolderHash differs
// from the current folder hash. Used by `thor validate` to decide
// whether to rebuild the map before running.
func IsStale(m *Map, currentHash string) bool {
	if m == nil {
		return true
	}
	if m.SchemaVersion != SchemaVersion {
		return true
	}
	return m.PartnerFolderHash != currentHash
}
