package runlog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ResultRecord is the JSON-friendly projection of a per-control validator
// result that the manifest captures. Keeping the runlog package
// independent of internal/validator avoids an import cycle and lets the
// validator import runlog (for the progress logger) without cycle issues.
//
// Callers translate validator.Result -> ResultRecord; the lossy fields
// (Err) are flattened to a string before serialization.
type ResultRecord struct {
	ControlID string `json:"controlId"`
	Status    string `json:"status"`
	Verdict   string `json:"verdict"`
	Reasoning string `json:"reasoning"`
	Error     string `json:"error,omitempty"`
}

// RunManifest is the auditable per-validation artifact PLAN.md 1F.2 pins.
// Written to <partner>/reports/run_<ts>.json after every validation.
type RunManifest struct {
	ID                string         `json:"id"`
	StartedAt         time.Time      `json:"startedAt"`
	CompletedAt       time.Time      `json:"completedAt"`
	ThorVersion       string         `json:"thorVersion"`
	ModelID           string         `json:"modelId"`
	PromptVersion     string         `json:"promptVersion"`
	PartnerFolderHash string         `json:"partnerFolderHash"`
	Controls          []string       `json:"controls"`
	Results           []ResultRecord `json:"results"`
}

// WriteManifest serializes m to <partnerFolder>/reports/run_<id>.json.
// The reports/ directory is created if it doesn't already exist. Returns
// the absolute path of the file written.
func WriteManifest(partnerFolder string, m RunManifest) (string, error) {
	reportsDir := filepath.Join(partnerFolder, "reports")
	if err := os.MkdirAll(reportsDir, 0o750); err != nil {
		return "", fmt.Errorf("runlog: mkdir %s: %w", reportsDir, err)
	}
	name := fmt.Sprintf("run_%s.json", manifestStamp(m))
	path := filepath.Join(reportsDir, name)
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("runlog: marshal manifest: %w", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", fmt.Errorf("runlog: write %s: %w", path, err)
	}
	return path, nil
}

// manifestStamp prefers a started-at timestamp (sortable on disk), and
// falls back to the run ID when StartedAt is zero.
func manifestStamp(m RunManifest) string {
	if !m.StartedAt.IsZero() {
		return m.StartedAt.UTC().Format("20060102_150405")
	}
	return m.ID
}

// HashPartnerFolder produces a short stable digest of the partner folder
// contents. Used in RunManifest.PartnerFolderHash so reviewers can detect
// whether two manifest runs targeted the same on-disk evidence.
//
// The digest covers the partner_responses.csv plus every file under
// supporting_docs/ (sorted by relative path, hashing path + size +
// content). Hidden files and Office tilde locks are skipped — same
// filter validator.listEvidenceFiles uses.
func HashPartnerFolder(partnerFolder string) (string, error) {
	h := sha256.New()

	// partner_responses.csv (optional — tolerate its absence so we can
	// still produce a manifest in degraded conditions).
	if data, err := os.ReadFile(filepath.Join(partnerFolder, "partner_responses.csv")); err == nil { //nolint:gosec // path is operator-supplied folder
		_, _ = fmt.Fprintf(h, "partner_responses.csv\x00%d\x00", len(data))
		_, _ = h.Write(data)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("runlog: read partner_responses.csv: %w", err)
	}

	supportingDir := filepath.Join(partnerFolder, "supporting_docs")
	entries, err := os.ReadDir(supportingDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("runlog: read %s: %w", supportingDir, err)
	}

	type fileEntry struct {
		rel  string
		full string
		size int64
	}
	var files []fileEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if isSkippedEvidence(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return "", fmt.Errorf("runlog: stat %s: %w", name, err)
		}
		files = append(files, fileEntry{
			rel:  filepath.Join("supporting_docs", name),
			full: filepath.Join(supportingDir, name),
			size: info.Size(),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	for _, f := range files {
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", f.rel, f.size)
		body, err := os.ReadFile(f.full) //nolint:gosec // path comes from operator-supplied folder
		if err != nil {
			return "", fmt.Errorf("runlog: read %s: %w", f.full, err)
		}
		_, _ = h.Write(body)
	}

	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func isSkippedEvidence(name string) bool {
	if len(name) >= 2 && name[:2] == "~$" {
		return true
	}
	if len(name) >= 1 && name[0] == '.' {
		return true
	}
	return false
}
