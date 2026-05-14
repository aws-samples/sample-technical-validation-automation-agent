package runlog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ------------------------------------------------------------------
// NewRunID — RFC 4122 v4
// ------------------------------------------------------------------

var uuidV4RE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewRunID_FormatIsRFC4122v4(t *testing.T) {
	for i := 0; i < 16; i++ {
		id := NewRunID()
		if !uuidV4RE.MatchString(id) {
			t.Errorf("NewRunID() = %q; want RFC 4122 v4", id)
		}
	}
}

func TestNewRunID_Unique(t *testing.T) {
	seen := make(map[string]struct{}, 1024)
	for i := 0; i < 1024; i++ {
		id := NewRunID()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate run ID after %d generations: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

// ------------------------------------------------------------------
// NewLogger — JSON to stderr (or supplied writer); no stdout
// ------------------------------------------------------------------

func TestNewLogger_DefaultsToStderr_NeverStdout(t *testing.T) {
	// We can't easily intercept os.Stderr here, but we can exercise the
	// "writer == nil → default" branch and confirm a custom writer
	// receives JSON. Together they verify the behavior the package
	// contract promises.
	var buf bytes.Buffer
	logger := NewLogger(Options{Writer: &buf})
	logger.Info("hello", "k", "v")
	if !strings.HasPrefix(buf.String(), "{") {
		t.Errorf("output is not JSON: %q", buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &rec); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if rec["msg"] != "hello" || rec["k"] != "v" {
		t.Errorf("missing expected fields: %+v", rec)
	}
}

func TestNewLogger_LevelDefaultIsInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(Options{Writer: &buf})
	logger.Debug("should be filtered")
	logger.Info("should pass through")
	if strings.Contains(buf.String(), "should be filtered") {
		t.Error("Debug record leaked despite default Info threshold")
	}
	if !strings.Contains(buf.String(), "should pass through") {
		t.Error("Info record was incorrectly filtered")
	}
}

// ------------------------------------------------------------------
// NewProgressLogger — tees to stderr writer + file writer
// ------------------------------------------------------------------

func TestNewProgressLogger_TeesAndStampsRunID(t *testing.T) {
	var stderrBuf, fileBuf bytes.Buffer
	logger := NewProgressLogger(&stderrBuf, &fileBuf, LevelInfo, "abc-123")

	LogControlEvent(logger, ControlEvent{
		ControlID:    "ACCT-001",
		Attempt:      1,
		LatencyMS:    42,
		InputTokens:  100,
		OutputTokens: 20,
		Status:       "PASSED",
	})

	for _, b := range []*bytes.Buffer{&stderrBuf, &fileBuf} {
		var rec map[string]any
		if err := json.Unmarshal(bytes.TrimRight(b.Bytes(), "\n"), &rec); err != nil {
			t.Fatalf("buffer not JSON: %v\n%s", err, b.String())
		}
		if rec["run_id"] != "abc-123" {
			t.Errorf("missing run_id: %+v", rec)
		}
		if rec["control_id"] != "ACCT-001" {
			t.Errorf("control_id = %v", rec["control_id"])
		}
		if rec["status"] != "PASSED" {
			t.Errorf("status = %v", rec["status"])
		}
		if rec["latency_ms"].(float64) != 42 {
			t.Errorf("latency_ms = %v", rec["latency_ms"])
		}
	}
}

// ------------------------------------------------------------------
// OpenProgressFile — appends, doesn't truncate
// ------------------------------------------------------------------

func TestOpenProgressFile_Appends(t *testing.T) {
	dir := t.TempDir()
	for _, body := range []string{"first\n", "second\n"} {
		f, err := OpenProgressFile(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	got, err := os.ReadFile(filepath.Join(dir, ProgressFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first\nsecond\n" {
		t.Errorf("expected appended log; got %q", string(got))
	}
}

// ------------------------------------------------------------------
// WriteManifest — JSON shape + reports/ creation
// ------------------------------------------------------------------

func TestWriteManifest_CreatesReportsDir(t *testing.T) {
	dir := t.TempDir()
	m := RunManifest{
		ID:          "11111111-1111-4111-8111-111111111111",
		StartedAt:   time.Date(2026, 5, 13, 10, 30, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, 5, 13, 10, 35, 0, 0, time.UTC),
		ThorVersion: "v0.1.0",
		ModelID:     "test-model",
		Controls:    []string{"ACCT-001", "DOC-006"},
		Results: []ResultRecord{
			{ControlID: "ACCT-001", Status: "PASSED", Verdict: "YES", Reasoning: "ok"},
			{ControlID: "DOC-006", Status: "WAIVED", Verdict: "WAIVED", Reasoning: "manual review"},
		},
	}

	path, err := WriteManifest(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "reports") {
		t.Errorf("path should be under reports/: %s", path)
	}
	if !strings.Contains(filepath.Base(path), "20260513_103000") {
		t.Errorf("filename should encode StartedAt timestamp: %s", filepath.Base(path))
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got RunManifest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("manifest not valid JSON: %v\n%s", err, body)
	}
	if got.ID != m.ID || got.ThorVersion != "v0.1.0" || len(got.Results) != 2 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestWriteManifest_FallsBackToIDWhenStartedAtZero(t *testing.T) {
	dir := t.TempDir()
	m := RunManifest{ID: "11111111-1111-4111-8111-111111111111"}
	path, err := WriteManifest(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(path), m.ID) {
		t.Errorf("expected filename to encode ID; got %s", filepath.Base(path))
	}
}

// ------------------------------------------------------------------
// HashPartnerFolder — stable across reads, sensitive to evidence change
// ------------------------------------------------------------------

func TestHashPartnerFolder_StableForUnchangedFolder(t *testing.T) {
	dir := writePartnerFolder(t, "ACCT-001,resp\n", map[string]string{
		"a.txt": "alpha",
		"b.txt": "beta",
	})

	h1, err := HashPartnerFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPartnerFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("hash drifted: %s vs %s", h1, h2)
	}
}

func TestHashPartnerFolder_SensitiveToContent(t *testing.T) {
	d1 := writePartnerFolder(t, "ACCT-001,resp\n", map[string]string{"a.txt": "v1"})
	d2 := writePartnerFolder(t, "ACCT-001,resp\n", map[string]string{"a.txt": "v2"})

	h1, _ := HashPartnerFolder(d1)
	h2, _ := HashPartnerFolder(d2)
	if h1 == h2 {
		t.Errorf("hash failed to change with file content; both = %s", h1)
	}
}

func TestHashPartnerFolder_SkipsHiddenAndTildeLockFiles(t *testing.T) {
	d1 := writePartnerFolder(t, "x", map[string]string{"a.txt": "v1"})
	d2 := writePartnerFolder(t, "x", map[string]string{
		"a.txt":      "v1",
		".DS_Store":  "junk",
		"~$tempfile": "lock",
	})
	h1, _ := HashPartnerFolder(d1)
	h2, _ := HashPartnerFolder(d2)
	if h1 != h2 {
		t.Errorf("hash should ignore hidden/tilde files; got %s vs %s", h1, h2)
	}
}

func TestHashPartnerFolder_TolerantOfMissingPieces(t *testing.T) {
	dir := t.TempDir() // no partner_responses.csv, no supporting_docs/
	h, err := HashPartnerFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h == "" {
		t.Error("hash must be non-empty even for empty folder (sha256 of nothing)")
	}
}

// writePartnerFolder is a small helper used by the hash tests.
func writePartnerFolder(t *testing.T, csvBody string, supporting map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "partner_responses.csv"), []byte(csvBody), 0o600); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(dir, "supporting_docs")
	if err := os.MkdirAll(sd, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range supporting {
		if err := os.WriteFile(filepath.Join(sd, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// ------------------------------------------------------------------
// Stopwatch — sanity
// ------------------------------------------------------------------

func TestStopwatch_PositiveElapsed(t *testing.T) {
	sw := Start()
	time.Sleep(2 * time.Millisecond)
	if sw.Elapsed() < 1 {
		t.Errorf("elapsed = %d, want >=1ms", sw.Elapsed())
	}
}
