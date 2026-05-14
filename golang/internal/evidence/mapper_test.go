package evidence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// stubBedrock answers Converse with the canned text for whichever
// filename appears in the user message. Tests configure responses by
// filename so concurrent calls land on the right answer. Pass-2 calls
// (which carry no filename header) are matched on a "control:<id>" key.
type stubBedrock struct {
	mu        sync.Mutex
	calls     atomic.Int64
	responses map[string]string // filename or "control:<id>" → assistant text
	fallback  string
	fail      map[string]error
}

func (s *stubBedrock) Converse(_ context.Context, in *bedrockruntime.ConverseInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseOutput, error) {
	s.calls.Add(1)
	var key string
	for _, b := range in.Messages[0].Content {
		t, ok := b.(*bedrocktypes.ContentBlockMemberText)
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(t.Value, "Filename:"):
			line := strings.TrimPrefix(t.Value, "Filename: ")
			line = strings.SplitN(line, "\n", 2)[0]
			key = strings.TrimSpace(line)
		case strings.HasPrefix(t.Value, "Control:"):
			line := strings.TrimPrefix(t.Value, "Control: ")
			line = strings.SplitN(line, "\n", 2)[0]
			key = "control:" + strings.TrimSpace(line)
		}
		if key != "" {
			break
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.fail[key]; ok {
		return nil, err
	}
	text := s.fallback
	if t, ok := s.responses[key]; ok {
		text = t
	}
	return &bedrockruntime.ConverseOutput{
		Output: &bedrocktypes.ConverseOutputMemberMessage{
			Value: bedrocktypes.Message{
				Content: []bedrocktypes.ContentBlock{
					&bedrocktypes.ContentBlockMemberText{Value: text},
				},
			},
		},
	}, nil
}

// stubFileProcessor returns a single text content block per file so the
// mapper has something to send. Bypasses the real validator routing.
type stubFileProcessor struct{}

func (stubFileProcessor) ProcessFileForEvidence(path string) ([]bedrocktypes.ContentBlock, error) {
	body, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	return []bedrocktypes.ContentBlock{
		&bedrocktypes.ContentBlockMemberDocument{Value: bedrocktypes.DocumentBlock{
			Name:   aws.String("doc"),
			Format: bedrocktypes.DocumentFormatTxt,
			Source: &bedrocktypes.DocumentSourceMemberBytes{Value: body},
		}},
	}, nil
}

// makePartnerFolder writes a partner folder with the supplied files
// inside supporting_docs/.
func makePartnerFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "partner_responses.csv"),
		[]byte("controlId,partner_response\nACCT-001,x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(dir, "supporting_docs")
	if err := os.MkdirAll(sd, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(sd, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// ------------------------------------------------------------------
// parsePass1Response — JSON envelope variants
// ------------------------------------------------------------------

func TestParsePass1_NewSchema(t *testing.T) {
	got, err := parsePass1Response(
		`{"assignments":[{"controlId":"ACCT-001","confidence":0.9},{"controlId":"COST-001","confidence":0.4}],"rationale":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Assignments) != 2 || got.Assignments[0].ControlID != "ACCT-001" {
		t.Errorf("got %+v", got.Assignments)
	}
	if got.Assignments[0].Confidence != 0.9 {
		t.Errorf("confidence lost: %v", got.Assignments[0].Confidence)
	}
}

func TestParsePass1_LegacyControlIDsAcceptedForCompat(t *testing.T) {
	got, err := parsePass1Response(`{"controlIds":["ACCT-001","COST-001"],"rationale":"y"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Assignments) != 2 {
		t.Fatalf("legacy form should populate Assignments; got %+v", got)
	}
	if got.Assignments[0].ControlID != "ACCT-001" {
		t.Errorf("first id = %q", got.Assignments[0].ControlID)
	}
}

func TestParsePass1_FencedMarkdown(t *testing.T) {
	raw := "```json\n{\"assignments\":[{\"controlId\":\"ACCT-001\",\"confidence\":0.7}],\"rationale\":\"y\"}\n```"
	got, err := parsePass1Response(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Assignments) != 1 || got.Assignments[0].ControlID != "ACCT-001" {
		t.Errorf("got %+v", got.Assignments)
	}
}

func TestParsePass1_NoJSONReturnsError(t *testing.T) {
	if _, err := parsePass1Response("sorry, can't help"); err == nil {
		t.Error("expected error when response has no JSON object")
	}
}

func TestParsePass2_StripsToFilenames(t *testing.T) {
	got, err := parsePass2Response(`{"files":["arch.pdf","sow.docx"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "arch.pdf" {
		t.Errorf("got %v", got)
	}
}

// ------------------------------------------------------------------
// BuildEvidenceMap end-to-end
// ------------------------------------------------------------------

func TestBuildEvidenceMap_DeclaredOnly_RanksByConfidence(t *testing.T) {
	folder := makePartnerFolder(t, map[string]string{
		"arch.txt":  "Architecture diagram showing AWS services",
		"sow.txt":   "Statement of work",
		"deck.txt":  "Marketing deck",
		"runbk.txt": "Production runbook",
	})

	stub := &stubBedrock{
		responses: map[string]string{
			"arch.txt": `{"assignments":[
				{"controlId":"DOC-001-SERVICE","confidence":0.95},
				{"controlId":"NETSEC-001","confidence":0.6}
			],"rationale":"diagram"}`,
			"sow.txt":   `{"assignments":[{"controlId":"USE-CASE-SERVICE","confidence":0.8}],"rationale":"sow"}`,
			"deck.txt":  `{"assignments":[{"controlId":"USE-CASE-SERVICE","confidence":0.5}],"rationale":"deck"}`,
			"runbk.txt": `{"assignments":[{"controlId":"OPE-001-SERVICE","confidence":0.9}],"rationale":"ops"}`,
		},
		fallback: `{"assignments":[],"rationale":"none"}`,
	}

	declared := []string{
		"DOC-001-SERVICE", "NETSEC-001", "USE-CASE-SERVICE", "OPE-001-SERVICE",
	}
	m, err := BuildEvidenceMap(context.Background(), folder, Options{
		Bedrock:          stub,
		FileProcessor:    stubFileProcessor{},
		Concurrency:      2,
		DeclaredControls: declared,
	})
	if err != nil {
		t.Fatal(err)
	}

	// USE-CASE-SERVICE should be ranked sow.txt (0.8) ahead of deck.txt (0.5).
	uc := m.Controls["USE-CASE-SERVICE"]
	if len(uc) != 2 || uc[0] != "sow.txt" || uc[1] != "deck.txt" {
		t.Errorf("USE-CASE-SERVICE rank order = %v, want [sow.txt deck.txt]", uc)
	}
	if got := m.Controls["DOC-001-SERVICE"]; len(got) != 1 || got[0] != "arch.txt" {
		t.Errorf("DOC-001-SERVICE = %v, want [arch.txt]", got)
	}
	if m.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", m.SchemaVersion, SchemaVersion)
	}
	if len(m.DeclaredControls) != len(declared) {
		t.Errorf("DeclaredControls = %v", m.DeclaredControls)
	}
}

func TestBuildEvidenceMap_DropsHallucinatedAndUndeclaredIDs(t *testing.T) {
	folder := makePartnerFolder(t, map[string]string{
		"a.txt": "hi",
	})
	stub := &stubBedrock{
		responses: map[string]string{
			// COST-001 is in CONTEXT.csv but NOT declared → must be dropped.
			// FAKE-999 is invented → must be dropped.
			"a.txt": `{"assignments":[
				{"controlId":"ACCT-001","confidence":0.9},
				{"controlId":"COST-001","confidence":0.5},
				{"controlId":"FAKE-999","confidence":0.9}
			],"rationale":"r"}`,
		},
	}

	m, err := BuildEvidenceMap(context.Background(), folder, Options{
		Bedrock:          stub,
		FileProcessor:    stubFileProcessor{},
		DeclaredControls: []string{"ACCT-001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Controls["FAKE-999"]; ok {
		t.Error("hallucinated control ID must not appear")
	}
	if _, ok := m.Controls["COST-001"]; ok {
		t.Error("undeclared control ID must not appear")
	}
	if got := m.Controls["ACCT-001"]; len(got) != 1 || got[0] != "a.txt" {
		t.Errorf("ACCT-001 = %v, want [a.txt]", got)
	}
}

func TestBuildEvidenceMap_FailedFileGoesToUnmapped(t *testing.T) {
	folder := makePartnerFolder(t, map[string]string{
		"good.txt": "hi",
		"bad.txt":  "world",
	})
	stub := &stubBedrock{
		responses: map[string]string{
			"good.txt": `{"assignments":[{"controlId":"ACCT-001","confidence":0.9}],"rationale":"r"}`,
		},
		fail: map[string]error{
			"bad.txt": errors.New("simulated bedrock error"),
		},
	}

	m, err := BuildEvidenceMap(context.Background(), folder, Options{
		Bedrock:          stub,
		FileProcessor:    stubFileProcessor{},
		DeclaredControls: []string{"ACCT-001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range m.Unmapped {
		if n == "bad.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("bad.txt should be in Unmapped; got %v", m.Unmapped)
	}
}

func TestBuildEvidenceMap_Pass2RepairsEmptyControls(t *testing.T) {
	folder := makePartnerFolder(t, map[string]string{
		"sow.txt":  "Statement of work",
		"runbk.txt": "Production runbook",
	})

	// Pass-1: only sow.txt gets a hit; runbk.txt returns nothing.
	// ACCT-001 ends up empty, triggering pass-2.
	stub := &stubBedrock{
		responses: map[string]string{
			"sow.txt":   `{"assignments":[{"controlId":"USE-CASE-SERVICE","confidence":0.9}],"rationale":"sow content"}`,
			"runbk.txt": `{"assignments":[],"rationale":"production runbook content covering account governance and ops"}`,
			// Pass-2 picks runbk.txt for ACCT-001.
			"control:ACCT-001": `{"files":["runbk.txt"]}`,
		},
	}
	m, err := BuildEvidenceMap(context.Background(), folder, Options{
		Bedrock:          stub,
		FileProcessor:    stubFileProcessor{},
		DeclaredControls: []string{"USE-CASE-SERVICE", "ACCT-001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Controls["ACCT-001"]; len(got) != 1 || got[0] != "runbk.txt" {
		t.Errorf("ACCT-001 should be repaired with [runbk.txt]; got %v", got)
	}
	if len(m.EmptyControls) != 0 {
		t.Errorf("EmptyControls should be empty after repair; got %v", m.EmptyControls)
	}
}

func TestBuildEvidenceMap_EmptyControlsRecordedWhenRepairFails(t *testing.T) {
	folder := makePartnerFolder(t, map[string]string{
		"sow.txt": "Statement of work",
	})
	stub := &stubBedrock{
		responses: map[string]string{
			"sow.txt": `{"assignments":[{"controlId":"USE-CASE-SERVICE","confidence":0.9}],"rationale":"sow"}`,
			// Pass-2 returns no picks for ACCT-001.
			"control:ACCT-001": `{"files":[]}`,
		},
	}
	m, err := BuildEvidenceMap(context.Background(), folder, Options{
		Bedrock:          stub,
		FileProcessor:    stubFileProcessor{},
		DeclaredControls: []string{"USE-CASE-SERVICE", "ACCT-001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Controls["ACCT-001"]) != 0 {
		t.Errorf("ACCT-001 should remain empty; got %v", m.Controls["ACCT-001"])
	}
	found := false
	for _, id := range m.EmptyControls {
		if id == "ACCT-001" {
			found = true
		}
	}
	if !found {
		t.Errorf("ACCT-001 should be in EmptyControls; got %v", m.EmptyControls)
	}
}

func TestBuildEvidenceMap_NoDeclaredControlsRejected(t *testing.T) {
	folder := makePartnerFolder(t, map[string]string{"a.txt": "hi"})
	_, err := BuildEvidenceMap(context.Background(), folder, Options{
		Bedrock:       &stubBedrock{},
		FileProcessor: stubFileProcessor{},
		// DeclaredControls intentionally empty.
	})
	if err == nil {
		t.Error("expected error when DeclaredControls is empty")
	}
}

// ------------------------------------------------------------------
// IsStale + LoadMap/SaveMap roundtrip
// ------------------------------------------------------------------

func TestSaveAndLoadMapRoundtrip(t *testing.T) {
	dir := t.TempDir()
	m := &Map{
		SchemaVersion:     SchemaVersion,
		PartnerFolderHash: "abc",
		DeclaredControls:  []string{"ACCT-001"},
		Controls:          map[string][]string{"ACCT-001": {"a.txt"}},
		Files:             map[string][]string{"a.txt": {"ACCT-001"}},
	}
	if _, err := SaveMap(dir, m); err != nil {
		t.Fatal(err)
	}
	got, err := LoadMap(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.PartnerFolderHash != "abc" {
		t.Errorf("PartnerFolderHash roundtrip lost: %q", got.PartnerFolderHash)
	}
	if got.Controls["ACCT-001"][0] != "a.txt" {
		t.Errorf("Controls roundtrip lost: %v", got.Controls)
	}
}

func TestLoadMap_Missing(t *testing.T) {
	got, err := LoadMap(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("missing file should yield nil map, got %+v", got)
	}
}

func TestIsStale(t *testing.T) {
	cur := "abc"
	cases := []struct {
		name string
		m    *Map
		want bool
	}{
		{"nil → stale", nil, true},
		{"hash match", &Map{SchemaVersion: SchemaVersion, PartnerFolderHash: cur}, false},
		{"hash mismatch", &Map{SchemaVersion: SchemaVersion, PartnerFolderHash: "other"}, true},
		{"schema mismatch", &Map{SchemaVersion: 999, PartnerFolderHash: cur}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsStale(c.m, cur); got != c.want {
				t.Errorf("IsStale = %v, want %v", got, c.want)
			}
		})
	}
}
