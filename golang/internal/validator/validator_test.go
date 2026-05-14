package validator

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"thor-golang/internal/prompts"
)

// ------------------------------------------------------------------
// Stubs
// ------------------------------------------------------------------

// stubBedrock is a minimal BedrockConverseAPI that records every Converse
// request and returns a canned response.
type stubBedrock struct {
	mu       sync.Mutex
	requests []*bedrockruntime.ConverseInput
	respond  func(in *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error)
}

func (s *stubBedrock) Converse(_ context.Context, in *bedrockruntime.ConverseInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseOutput, error) {
	s.mu.Lock()
	s.requests = append(s.requests, in)
	resp := s.respond
	s.mu.Unlock()
	return resp(in)
}

// stubMarketplace is a deterministic MarketplaceChecker for unit tests.
type stubMarketplace struct {
	last   string
	result Result
}

func (s *stubMarketplace) Check(_ context.Context, partnerResp string) Result {
	s.last = partnerResp
	return s.result
}

// canned builds a Bedrock response whose assistant message is text.
func canned(text string) *bedrockruntime.ConverseOutput {
	return &bedrockruntime.ConverseOutput{
		Output: &bedrocktypes.ConverseOutputMemberMessage{
			Value: bedrocktypes.Message{
				Role: bedrocktypes.ConversationRoleAssistant,
				Content: []bedrocktypes.ContentBlock{
					&bedrocktypes.ContentBlockMemberText{Value: text},
				},
			},
		},
		Usage: &bedrocktypes.TokenUsage{
			InputTokens:  aws.Int32(100),
			OutputTokens: aws.Int32(20),
			TotalTokens:  aws.Int32(120),
		},
	}
}

// makePartnerFolder writes the partner_responses.csv structure validate
// expects. supportingFiles is a map of filename → contents written under
// supporting_docs/.
func makePartnerFolder(t *testing.T, controlID, partnerResp string, supportingFiles map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()

	csvPath := filepath.Join(dir, "partner_responses.csv")
	f, err := os.Create(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	w := csv.NewWriter(f)
	if err := w.Write([]string{"controlId", "partner_response"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Write([]string{controlID, partnerResp}); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if len(supportingFiles) > 0 {
		sd := filepath.Join(dir, "supporting_docs")
		if err := os.MkdirAll(sd, 0o750); err != nil {
			t.Fatal(err)
		}
		for name, body := range supportingFiles {
			if err := os.WriteFile(filepath.Join(sd, name), body, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

// newTestValidator returns a Validator wired to the supplied stubs.
func newTestValidator(t *testing.T, bedrockStub *stubBedrock, mkt MarketplaceChecker) *Validator {
	t.Helper()
	v, err := NewValidator(Options{
		Bedrock:     bedrockStub,
		Marketplace: mkt,
		AWSConfig:   aws.Config{Region: "us-east-1"}, // unused when Bedrock is overridden
		ModelID:     "test-model",
		Concurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// ------------------------------------------------------------------
// ValidateControl: request shape + result parsing
// ------------------------------------------------------------------

func TestValidateControl_BuildsExpectedConverseRequest(t *testing.T) {
	stub := &stubBedrock{
		respond: func(*bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			return canned("YES. partner satisfies the requirement"), nil
		},
	}
	v := newTestValidator(t, stub, &stubMarketplace{})
	folder := makePartnerFolder(t, "ACCT-001", "we maintain an AWS account dedicated to compliance.", nil)

	r := v.ValidateControl(context.Background(), "ACCT-001", folder)
	if r.Status != StatusPassed {
		t.Fatalf("status = %v, reasoning = %q", r.Status, r.Reasoning)
	}

	if len(stub.requests) != 1 {
		t.Fatalf("Converse calls = %d, want 1 (B6: no within-control batching)", len(stub.requests))
	}
	in := stub.requests[0]
	if aws.ToString(in.ModelId) != "test-model" {
		t.Errorf("ModelId = %q", aws.ToString(in.ModelId))
	}
	if in.InferenceConfig == nil ||
		aws.ToInt32(in.InferenceConfig.MaxTokens) != 2000 ||
		aws.ToFloat32(in.InferenceConfig.Temperature) != 0.0 {
		t.Errorf("InferenceConfig = %+v, want MaxTokens=2000, Temperature=0", in.InferenceConfig)
	}
	if len(in.System) != 1 {
		t.Fatalf("System blocks = %d, want 1", len(in.System))
	}
	sysText := in.System[0].(*bedrocktypes.SystemContentBlockMemberText).Value
	if !strings.Contains(sysText, "AWS Partner") {
		head := sysText
		if len(head) > 80 {
			head = head[:80]
		}
		t.Errorf("system prompt missing expected substring; got %q", head)
	}
	if len(in.Messages) != 1 || in.Messages[0].Role != bedrocktypes.ConversationRoleUser {
		t.Fatalf("Messages = %+v", in.Messages)
	}
	// Content ordering: prompt_context (text), partner-response (text), [evidence], closing question (text).
	content := in.Messages[0].Content
	if len(content) < 3 {
		t.Fatalf("content blocks = %d, want >=3", len(content))
	}
	first := content[0].(*bedrocktypes.ContentBlockMemberText).Value
	if first == "" {
		t.Error("first content block (prompt_context) must not be empty")
	}
	second := content[1].(*bedrocktypes.ContentBlockMemberText).Value
	if !strings.Contains(second, "ACCT-001") {
		t.Errorf("second block must reference controlId; got %q", second)
	}
	last := content[len(content)-1].(*bedrocktypes.ContentBlockMemberText).Value
	if !strings.Contains(last, "approved") {
		t.Errorf("last block must be the closing question; got %q", last)
	}
}

func TestValidateControl_DispatchesToMarketplaceForDOC006(t *testing.T) {
	mkt := &stubMarketplace{result: Result{Status: StatusWaived, Reasoning: "test waive"}}
	stub := &stubBedrock{
		respond: func(*bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			t.Fatal("Bedrock must not be called for DOC-006")
			return nil, nil
		},
	}
	v := newTestValidator(t, stub, mkt)
	folder := makePartnerFolder(t, "DOC-006", "we are not on AWS Marketplace", nil)

	r := v.ValidateControl(context.Background(), "DOC-006", folder)
	if r.Status != StatusWaived || r.ControlID != "DOC-006" {
		t.Errorf("got %+v", r)
	}
	if mkt.last != "we are not on AWS Marketplace" {
		t.Errorf("MarketplaceChecker received %q", mkt.last)
	}
}

func TestValidateControl_MissingPartnerResponseReturnsFAIL(t *testing.T) {
	stub := &stubBedrock{
		respond: func(*bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			return canned("YES. ok"), nil
		},
	}
	v := newTestValidator(t, stub, &stubMarketplace{})
	folder := makePartnerFolder(t, "OTHER-001", "x", nil) // populated, but for a different control

	r := v.ValidateControl(context.Background(), "ACCT-001", folder)
	if r.Status != StatusFailed {
		t.Errorf("status = %v, want StatusFailed for missing partner response", r.Status)
	}
	if len(stub.requests) != 0 {
		t.Errorf("Bedrock should not have been called when partner response is missing; got %d", len(stub.requests))
	}
}

func TestValidateControl_BedrockErrorBecomesERRORED_B11(t *testing.T) {
	stub := &stubBedrock{
		respond: func(*bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			return nil, errors.New("throttled")
		},
	}
	v := newTestValidator(t, stub, &stubMarketplace{})
	folder := makePartnerFolder(t, "ACCT-001", "details", nil)

	r := v.ValidateControl(context.Background(), "ACCT-001", folder)
	if r.Status != StatusErrored {
		t.Fatalf("status = %v, want StatusErrored (B11)", r.Status)
	}
	if r.Err == nil {
		t.Error("Err must be non-nil for ERRORED (B11)")
	}
	if r.Reasoning == "" {
		t.Error("Reasoning must be populated even on error (B11)")
	}
}

// ------------------------------------------------------------------
// SystemMode pickup — proves all three modes embed
// ------------------------------------------------------------------

func TestNewValidator_SystemModeWiresEmbeddedPrompt(t *testing.T) {
	for _, mode := range []prompts.SystemMode{prompts.SystemOld, prompts.SystemNew, prompts.SystemRevised} {
		v, err := NewValidator(Options{
			AWSConfig: aws.Config{Region: "us-east-1"},
			Bedrock: &stubBedrock{respond: func(*bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
				return canned("YES. ok"), nil
			}},
			SystemMode: mode,
		})
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if len(v.systemPrompt) == 0 {
			t.Errorf("mode %q: systemPrompt is empty", mode)
		}
	}
}

func TestNewValidator_RejectsUnknownMode(t *testing.T) {
	_, err := NewValidator(Options{
		AWSConfig:  aws.Config{Region: "us-east-1"},
		Bedrock:    &stubBedrock{},
		SystemMode: "bogus",
	})
	if err == nil {
		t.Fatal("want error for unknown SystemMode")
	}
}

// ------------------------------------------------------------------
// ValidateBatch: parallelism + progress
// ------------------------------------------------------------------

func TestValidateBatch_ProcessesAllControls(t *testing.T) {
	var calls atomic.Int64
	stub := &stubBedrock{
		respond: func(in *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			calls.Add(1)
			// Look at the partner-response text block to figure out which control.
			for _, b := range in.Messages[0].Content {
				if t, ok := b.(*bedrocktypes.ContentBlockMemberText); ok && strings.Contains(t.Value, "submitted by Partner") {
					if strings.Contains(t.Value, "ACCT-001") {
						return canned("YES. acct passed"), nil
					}
					if strings.Contains(t.Value, "ACCT-002") {
						return canned("NO. missing evidence"), nil
					}
				}
			}
			return canned("NO. unknown"), nil
		},
	}
	v := newTestValidator(t, stub, &stubMarketplace{})
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "partner_responses.csv")
	body := "controlId,partner_response\nACCT-001,acct1 details\nACCT-002,acct2 details\n"
	if err := os.WriteFile(csvPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var progressEvents int64
	results, err := v.ValidateBatch(context.Background(), []string{"ACCT-001", "ACCT-002"}, dir,
		BatchOptions{Concurrency: 2, Progress: func(_ ProgressEvent) { atomic.AddInt64(&progressEvents, 1) }})
	if err != nil {
		t.Fatal(err)
	}
	if results["ACCT-001"].Status != StatusPassed {
		t.Errorf("ACCT-001: got %+v", results["ACCT-001"])
	}
	if results["ACCT-002"].Status != StatusFailed {
		t.Errorf("ACCT-002: got %+v", results["ACCT-002"])
	}
	if calls.Load() != 2 {
		t.Errorf("Converse calls = %d, want 2", calls.Load())
	}
	if atomic.LoadInt64(&progressEvents) != 2 {
		t.Errorf("progress events = %d, want 2", atomic.LoadInt64(&progressEvents))
	}
}

// ------------------------------------------------------------------
// Consensus: B7 early-stop
// ------------------------------------------------------------------

func TestValidateBatch_Consensus_AllPassEarlyStops_B7(t *testing.T) {
	// All controls return YES on every run. Runs == 3 means we must
	// observe exactly 2 runs × N controls Converse calls (early stop).
	var calls atomic.Int64
	stub := &stubBedrock{
		respond: func(*bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			calls.Add(1)
			return canned("YES. always pass"), nil
		},
	}
	v := newTestValidator(t, stub, &stubMarketplace{})
	dir := t.TempDir()
	body := "controlId,partner_response\nACCT-001,a\nACCT-002,b\n"
	if err := os.WriteFile(filepath.Join(dir, "partner_responses.csv"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := v.ValidateBatch(context.Background(), []string{"ACCT-001", "ACCT-002"}, dir,
		BatchOptions{ConsensusRuns: 3})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Errorf("Converse calls = %d, want 4 (2 controls × 2 runs after early-stop) (B7)", calls.Load())
	}
	for id, r := range results {
		if r.Status != StatusPassed {
			t.Errorf("%s: status = %v, want StatusPassed", id, r.Status)
		}
	}
}

func TestValidateBatch_Consensus_MixedRunsCompleteFully_B7(t *testing.T) {
	// One control fails on run 2 → no early stop, so we expect 3 full runs.
	var run atomic.Int64
	stub := &stubBedrock{
		respond: func(in *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			r := run.Add(1)
			// runs 1+2 of ACCT-002 is YES then NO -> not all-pass-twice -> no early stop
			text := in.Messages[0].Content[1].(*bedrocktypes.ContentBlockMemberText).Value
			isAcct002 := strings.Contains(text, "ACCT-002")
			switch {
			case isAcct002 && r > 2 && r <= 4:
				return canned("NO. flaky"), nil
			default:
				return canned("YES. ok"), nil
			}
		},
	}
	v := newTestValidator(t, stub, &stubMarketplace{})
	dir := t.TempDir()
	body := "controlId,partner_response\nACCT-001,a\nACCT-002,b\n"
	if err := os.WriteFile(filepath.Join(dir, "partner_responses.csv"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := v.ValidateBatch(context.Background(), []string{"ACCT-001", "ACCT-002"}, dir,
		BatchOptions{ConsensusRuns: 3})
	if err != nil {
		t.Fatal(err)
	}
	if results["ACCT-001"].Status != StatusPassed {
		t.Errorf("ACCT-001: got %+v", results["ACCT-001"])
	}
	if !strings.Contains(results["ACCT-001"].Reasoning, "Consensus") {
		t.Errorf("Reasoning should mention Consensus; got %q", results["ACCT-001"].Reasoning)
	}
}

// ------------------------------------------------------------------
// EvidenceFilter wiring
// ------------------------------------------------------------------

func TestValidateControl_EvidenceFilter_FiltersBasenames(t *testing.T) {
	// Capture the filenames the validator sent by inspecting the
	// document blocks the stub Bedrock receives. The mock filter only
	// admits "keep.txt" — "drop.txt" must not appear in the request.
	stub := &stubBedrock{
		respond: func(*bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			return canned("YES. ok"), nil
		},
	}

	v, err := NewValidator(Options{
		Bedrock:     stub,
		Marketplace: &stubMarketplace{},
		AWSConfig:   aws.Config{Region: "us-east-1"},
		ModelID:     "test-model",
		Concurrency: 4,
		EvidenceFilter: func(controlID string) ([]string, bool) {
			if controlID == "ACCT-001" {
				return []string{"keep.txt"}, true
			}
			return nil, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	folder := makePartnerFolder(t, "ACCT-001", "details", map[string][]byte{
		"keep.txt": []byte("kept payload"),
		"drop.txt": []byte("dropped payload"),
	})

	r := v.ValidateControl(context.Background(), "ACCT-001", folder)
	if r.Status != StatusPassed {
		t.Fatalf("status = %v, reasoning = %q", r.Status, r.Reasoning)
	}

	if len(stub.requests) != 1 {
		t.Fatalf("Converse calls = %d, want 1", len(stub.requests))
	}
	// Walk the request blocks; document blocks carry no original filename
	// (safeDocName randomises) so we check the txt-document payload bytes
	// to confirm only "kept payload" was sent.
	var docBytes [][]byte
	for _, b := range stub.requests[0].Messages[0].Content {
		if d, ok := b.(*bedrocktypes.ContentBlockMemberDocument); ok {
			if src, ok := d.Value.Source.(*bedrocktypes.DocumentSourceMemberBytes); ok {
				docBytes = append(docBytes, src.Value)
			}
		}
	}
	if len(docBytes) != 1 {
		t.Fatalf("document blocks = %d, want 1 (filtered)", len(docBytes))
	}
	if string(docBytes[0]) != "kept payload" {
		t.Errorf("filter let wrong file through: payload = %q", string(docBytes[0]))
	}
}

// EvidenceFilter ordering: the validator must send files in the order
// the filter returned them, NOT alphabetical OS-readdir order. This is
// what makes the 5-doc cap drop the *least relevant* files into the
// appendix when the mapper produced a ranking.
func TestValidateControl_EvidenceFilter_PreservesFilterOrder(t *testing.T) {
	stub := &stubBedrock{
		respond: func(*bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			return canned("YES. ok"), nil
		},
	}
	// Filter returns the reverse-alphabetical order (z.txt, y.txt, x.txt)
	// so any test that read os.ReadDir order would fail.
	v, err := NewValidator(Options{
		Bedrock:     stub,
		Marketplace: &stubMarketplace{},
		AWSConfig:   aws.Config{Region: "us-east-1"},
		EvidenceFilter: func(_ string) ([]string, bool) {
			return []string{"z.txt", "y.txt", "x.txt"}, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	folder := makePartnerFolder(t, "ACCT-001", "details", map[string][]byte{
		"x.txt": []byte("XXX"),
		"y.txt": []byte("YYY"),
		"z.txt": []byte("ZZZ"),
	})
	v.ValidateControl(context.Background(), "ACCT-001", folder)

	var payloads []string
	for _, b := range stub.requests[0].Messages[0].Content {
		if d, ok := b.(*bedrocktypes.ContentBlockMemberDocument); ok {
			if src, ok := d.Value.Source.(*bedrocktypes.DocumentSourceMemberBytes); ok {
				payloads = append(payloads, string(src.Value))
			}
		}
	}
	want := []string{"ZZZ", "YYY", "XXX"}
	if fmt.Sprintf("%v", payloads) != fmt.Sprintf("%v", want) {
		t.Errorf("filter order lost: got %v, want %v", payloads, want)
	}
}

// EvidenceFilter empty-list semantics: hasMap=true with an empty []
// means "send no evidence files" (declared-but-no-evidence path). The
// validator must NOT fall back to the unfiltered set.
func TestValidateControl_EvidenceFilter_EmptyListSendsNoFiles(t *testing.T) {
	stub := &stubBedrock{
		respond: func(*bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			return canned("NO. no evidence"), nil
		},
	}
	v, err := NewValidator(Options{
		Bedrock:     stub,
		Marketplace: &stubMarketplace{},
		AWSConfig:   aws.Config{Region: "us-east-1"},
		EvidenceFilter: func(_ string) ([]string, bool) {
			return []string{}, true // empty + hasMap=true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	folder := makePartnerFolder(t, "ACCT-001", "details", map[string][]byte{
		"x.txt": []byte("XXX"),
		"y.txt": []byte("YYY"),
	})
	v.ValidateControl(context.Background(), "ACCT-001", folder)

	doc := 0
	for _, b := range stub.requests[0].Messages[0].Content {
		if _, ok := b.(*bedrocktypes.ContentBlockMemberDocument); ok {
			doc++
		}
	}
	if doc != 0 {
		t.Errorf("hasMap=true with empty list should send no docs; got %d", doc)
	}
}

func TestValidateControl_EvidenceFilter_NoMapPassesEverything(t *testing.T) {
	// hasMap=false → unfiltered (graceful fallback).
	stub := &stubBedrock{
		respond: func(*bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			return canned("YES. ok"), nil
		},
	}
	v, err := NewValidator(Options{
		Bedrock:     stub,
		Marketplace: &stubMarketplace{},
		AWSConfig:   aws.Config{Region: "us-east-1"},
		EvidenceFilter: func(_ string) ([]string, bool) {
			return nil, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	folder := makePartnerFolder(t, "ACCT-001", "details", map[string][]byte{
		"a.txt": []byte("aaa"),
		"b.txt": []byte("bbb"),
	})
	v.ValidateControl(context.Background(), "ACCT-001", folder)

	doc := 0
	for _, b := range stub.requests[0].Messages[0].Content {
		if _, ok := b.(*bedrocktypes.ContentBlockMemberDocument); ok {
			doc++
		}
	}
	if doc != 2 {
		t.Errorf("filter hasMap=false should leave all files; got %d docs", doc)
	}
}

// ------------------------------------------------------------------
// Sanity for SortControlIDs helper (covered by use, but explicit anyway)
// ------------------------------------------------------------------

func TestSortControlIDs(t *testing.T) {
	got := SortControlIDs([]string{"B", "A", "C"})
	want := []string{"A", "B", "C"}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
