package mcpserver

import (
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"thor-golang/internal/prompts"
	"thor-golang/internal/validator"
)

// discardLogger keeps test output clean by routing slog records to io.Discard.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// ------------------------------------------------------------------
// Stubs
// ------------------------------------------------------------------

type stubBedrock struct {
	mu      sync.Mutex
	calls   int
	respond string
}

func (s *stubBedrock) Converse(_ context.Context, _ *bedrockruntime.ConverseInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseOutput, error) {
	s.mu.Lock()
	s.calls++
	resp := s.respond
	s.mu.Unlock()
	return &bedrockruntime.ConverseOutput{
		Output: &bedrocktypes.ConverseOutputMemberMessage{
			Value: bedrocktypes.Message{
				Role: bedrocktypes.ConversationRoleAssistant,
				Content: []bedrocktypes.ContentBlock{
					&bedrocktypes.ContentBlockMemberText{Value: resp},
				},
			},
		},
		Usage: &bedrocktypes.TokenUsage{
			InputTokens:  aws.Int32(1),
			OutputTokens: aws.Int32(1),
			TotalTokens:  aws.Int32(2),
		},
	}, nil
}

type stubMarketplace struct{}

func (stubMarketplace) Check(_ context.Context, _ string) validator.Result {
	return validator.Result{Status: validator.StatusPassed, Reasoning: "stub"}
}

func stubFactory(brStub *stubBedrock) ValidatorFactory {
	return func(_ context.Context, mode prompts.SystemMode, _ int,
		filter validator.EvidenceFilter) (*validator.Validator, error) {
		return validator.NewValidator(validator.Options{
			AWSConfig:      aws.Config{Region: "us-east-1"},
			Bedrock:        brStub,
			Marketplace:    stubMarketplace{},
			SystemMode:     mode,
			Concurrency:    4,
			EvidenceFilter: filter,
		})
	}
}

// ------------------------------------------------------------------
// Harness — boots the MCP server in-process via InMemoryTransport
// and returns a connected ClientSession plus the validator stub.
// ------------------------------------------------------------------

type harness struct {
	t       *testing.T
	session *mcp.ClientSession
	bedrock *stubBedrock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	br := &stubBedrock{respond: "YES. stub pass"}

	srv := newServer()
	registerTools(srv, Options{Validator: stubFactory(br)},
		// silent logger keeps test output clean
		discardLogger())

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()

	// Start the server first (per SDK doc).
	go func() {
		_ = srv.Run(ctx, serverT)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return &harness{t: t, session: session, bedrock: br}
}

// callTool is a small wrapper that fails the test on transport error.
func (h *harness) callTool(name string, args map[string]any) *mcp.CallToolResult {
	h.t.Helper()
	res, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		h.t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func contentText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty CallToolResult.Content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

// ------------------------------------------------------------------
// ListTools — schema/inventory check (PLAN.md 3.2: 7 tools + thor_map)
// ------------------------------------------------------------------

func TestListTools_AllRegistered(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(res.Tools))
	for _, x := range res.Tools {
		got = append(got, x.Name)
	}
	sort.Strings(got)
	want := []string{
		"thor_convert", "thor_diff", "thor_doctor", "thor_export",
		"thor_list_controls", "thor_map", "thor_prep", "thor_validate",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v, want %v", got, want)
	}
}

// ------------------------------------------------------------------
// thor_list_controls — pure read-only, no AWS
// ------------------------------------------------------------------

func TestThorListControls_ReturnsEntries(t *testing.T) {
	h := newHarness(t)
	res := h.callTool("thor_list_controls", nil)
	body := contentText(t, res)
	if !strings.Contains(body, "ACCT-001") {
		t.Errorf("expected ACCT-001 in list-controls output; got\n%s", body)
	}
}

func TestThorListControls_FilteredByCategory(t *testing.T) {
	h := newHarness(t)
	res := h.callTool("thor_list_controls", map[string]any{
		"category": "Generative AI Applications",
	})
	body := contentText(t, res)
	if !strings.Contains(body, "GAIAPP-001") {
		t.Errorf("expected GAIAPP-001 in filtered listing")
	}
	if strings.Contains(body, "GAIFMS-001") {
		t.Errorf("filter leaked: GAIFMS-001 should not appear")
	}
}

// ------------------------------------------------------------------
// thor_prep — folder skeleton
// ------------------------------------------------------------------

func TestThorPrep_CreatesFolder(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t)
	res := h.callTool("thor_prep", map[string]any{
		"partner_name": "Acme",
		"category":     "Generative AI Applications",
		"folder":       dir,
	})
	body := contentText(t, res)
	if !strings.Contains(body, "Created") {
		t.Errorf("expected confirmation; got %q", body)
	}
	for _, sub := range []string{
		filepath.Join(dir, "Acme", "supporting_docs"),
		filepath.Join(dir, "Acme", "reports", "summary"),
	} {
		if _, err := os.Stat(sub); err != nil {
			t.Errorf("missing %s: %v", sub, err)
		}
	}
}

func TestThorPrep_MissingCategoryReturnsError(t *testing.T) {
	h := newHarness(t)
	res := h.callTool("thor_prep", map[string]any{
		"partner_name": "Acme",
		"folder":       t.TempDir(),
	})
	if !res.IsError {
		t.Error("expected IsError when category is missing")
	}
}

// ------------------------------------------------------------------
// thor_validate — full pipeline through MCP, stubbed Bedrock
// ------------------------------------------------------------------

func TestThorValidate_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	writePartnerFolder(t, dir, "ACCT-001", "we maintain dedicated AWS accounts.")

	h := newHarness(t)
	res := h.callTool("thor_validate", map[string]any{
		"partner_folder":  dir,
		"skip_conversion": true,
		"no_map":          true,
	})
	body := contentText(t, res)
	if !strings.Contains(body, "Validated 1 control(s)") {
		t.Errorf("unexpected validate body: %q", body)
	}
	if h.bedrock.calls != 1 {
		t.Errorf("bedrock calls = %d, want 1", h.bedrock.calls)
	}
	// Reports are written to disk.
	if _, err := os.Stat(filepath.Join(dir, "validation_summary.md")); err != nil {
		t.Errorf("expected validation_summary.md: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "reports", "summary", "validation_summary_*.md"))
	if len(matches) == 0 {
		t.Error("expected timestamped report")
	}
	manifests, _ := filepath.Glob(filepath.Join(dir, "reports", "run_*.json"))
	if len(manifests) == 0 {
		t.Error("expected run manifest")
	}
}

// ------------------------------------------------------------------
// 3.7 — concurrent tool calls. Validator must be goroutine-safe.
// ------------------------------------------------------------------

func TestConcurrent_ThreeSimultaneousValidates(t *testing.T) {
	h := newHarness(t)
	const N = 3

	dirs := make([]string, N)
	for i := 0; i < N; i++ {
		dirs[i] = t.TempDir()
		writePartnerFolder(t, dirs[i], "ACCT-001", "shared response")
	}

	var wg sync.WaitGroup
	results := make([]*mcp.CallToolResult, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			r, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "thor_validate",
				Arguments: map[string]any{
					"partner_folder":  dirs[idx],
					"skip_conversion": true,
					"no_map":          true,
				},
			})
			if err != nil {
				t.Errorf("call %d: %v", idx, err)
				return
			}
			results[idx] = r
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if r == nil {
			t.Fatalf("call %d returned nil", i)
		}
		if r.IsError {
			t.Errorf("call %d: %s", i, contentText(t, r))
		}
	}
	if h.bedrock.calls != N {
		t.Errorf("bedrock calls = %d, want %d", h.bedrock.calls, N)
	}
}

// ------------------------------------------------------------------
// Stdio sanity — Run() never writes to os.Stdout
// ------------------------------------------------------------------

func TestRun_NeverWritesToStdout(t *testing.T) {
	// Replace os.Stdout briefly. Fail the test if anything is written.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = origStdout
		_ = w.Close()
	})

	// Boot a server on InMemoryTransport, do nothing, shut down.
	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = Run(ctx, Options{
			Transport: serverT,
			Stderr:    &bytes.Buffer{},
			Validator: stubFactory(&stubBedrock{respond: "YES."}),
		})
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "audit", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	_ = session.Close()
	cancel()
	_ = w.Close()
	os.Stdout = origStdout

	var captured bytes.Buffer
	_, _ = captured.ReadFrom(r)
	if captured.Len() > 0 {
		t.Errorf("Run() wrote %d byte(s) to stdout: %q", captured.Len(), captured.String())
	}
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func writePartnerFolder(t *testing.T, dir, controlID, response string) {
	t.Helper()
	csvPath := filepath.Join(dir, "partner_responses.csv")
	f, err := os.Create(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	w := csv.NewWriter(f)
	if err := w.Write([]string{"controlId", "partner_response"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Write([]string{controlID, response}); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
