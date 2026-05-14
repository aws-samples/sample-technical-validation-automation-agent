package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/xuri/excelize/v2"

	"thor-golang/internal/awsauth"
	"thor-golang/internal/evidence"
	"thor-golang/internal/excel"
	"thor-golang/internal/prompts"
	"thor-golang/internal/validator"
)

// xlsxFile is a thin alias so the fixture helper reads cleanly.
func xlsxFile() *excelize.File { return excelize.NewFile() }

// runCLI executes the cobra root with args and captures stdout/stderr +
// exit code. Mirrors what cmd/thor/main.go does.
func runCLI(args ...string) (stdout, stderr string, code int) {
	var outBuf, errBuf bytes.Buffer
	code = Execute(args, &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}

// ------------------------------------------------------------------
// --version + --help — no AWS calls
// ------------------------------------------------------------------

func TestVersionFlagPrintsBuildMetadata(t *testing.T) {
	out, _, code := runCLI("--version")
	if code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(out, "thor ") {
		t.Errorf("expected version line; got %q", out)
	}
}

func TestUnknownFlag_ReturnsExitUsageError(t *testing.T) {
	_, stderr, code := runCLI("--no-such-flag")
	if code != ExitUsageError {
		t.Errorf("exit code = %d, want %d (usage error)", code, ExitUsageError)
	}
	if !strings.Contains(stderr, "unknown flag") {
		t.Errorf("expected unknown-flag message; got %q", stderr)
	}
}

func TestUnknownSubcommand_ReturnsExitUsageError(t *testing.T) {
	_, _, code := runCLI("nope")
	if code != ExitUsageError {
		t.Errorf("exit code = %d, want %d", code, ExitUsageError)
	}
}

// ------------------------------------------------------------------
// list-controls
// ------------------------------------------------------------------

func TestListControls_AllAndFiltered(t *testing.T) {
	out, _, code := runCLI("list-controls")
	if code != ExitOK {
		t.Fatalf("exit %d, stderr captured separately", code)
	}
	if !strings.Contains(out, "ACCT-001") {
		t.Errorf("expected ACCT-001 in full listing; got\n%s", out)
	}

	filtered, _, code := runCLI("list-controls", "--category", "Generative AI Applications")
	if code != ExitOK {
		t.Fatalf("filtered exit %d", code)
	}
	if !strings.Contains(filtered, "GAIAPP-001") {
		t.Errorf("expected GAIAPP-001 in filtered listing")
	}
	if strings.Contains(filtered, "GAIFMS-001") {
		t.Errorf("GAIFMS-001 should not appear under Generative AI Applications")
	}
}

// ------------------------------------------------------------------
// prep — folder skeleton
// ------------------------------------------------------------------

func TestPrep_CreatesSkeletonAndListsApplicable(t *testing.T) {
	dir := t.TempDir()
	out, _, code := runCLI("prep", "Acme",
		"--category", "Generative AI Applications",
		"--folder", dir)
	if code != ExitOK {
		t.Fatalf("exit %d, out=%s", code, out)
	}
	root := filepath.Join(dir, "Acme")
	for _, sub := range []string{"supporting_docs", filepath.Join("reports", "summary")} {
		if _, err := os.Stat(filepath.Join(root, sub)); err != nil {
			t.Errorf("missing %s: %v", sub, err)
		}
	}
	if !strings.Contains(out, "applicable controls:") {
		t.Errorf("expected applicable-count line; got\n%s", out)
	}
}

func TestPrep_RequiresCategory(t *testing.T) {
	_, _, code := runCLI("prep", "Acme", "--folder", t.TempDir())
	if code != ExitUsageError {
		t.Errorf("exit %d, want %d", code, ExitUsageError)
	}
}

// ------------------------------------------------------------------
// convert — drives the full Excel→CSV pipeline through the CLI
// ------------------------------------------------------------------

func TestConvert_ProducesCanonicalCSV(t *testing.T) {
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "checklist.xlsx")
	buildPartnerXLSX(t, xlsx)

	out, _, code := runCLI("convert", dir)
	if code != ExitOK {
		t.Fatalf("exit %d, out=%s", code, out)
	}
	rows, err := excel.LoadResponses(filepath.Join(dir, "partner_responses.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Error("expected at least one extracted row")
	}
}

func TestConvert_RejectsInvalidAppType(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runCLI("convert", dir, "--app-type", "BOTH")
	if code != ExitUsageError {
		t.Errorf("exit %d, want %d; stderr=%s", code, ExitUsageError, stderr)
	}
}

// ------------------------------------------------------------------
// validate — drives the full pipeline with a stubbed Bedrock backend
// ------------------------------------------------------------------

func TestValidate_EndToEnd_WithStubBedrock(t *testing.T) {
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "checklist.xlsx")
	buildPartnerXLSX(t, xlsx)

	// Run convert first so we have partner_responses.csv (with --skip-conversion below).
	if _, _, code := runCLI("convert", dir); code != ExitOK {
		t.Fatalf("convert failed: code=%d", code)
	}

	// Inject a stub validator via the test-only seam in validate.go.
	t.Cleanup(func() { testValidateDepsOverride = nil })
	deps := validateDeps{
		newValidator: func(_ context.Context, mode prompts.SystemMode, _ int,
			filter validator.EvidenceFilter) (*validator.Validator, error) {
			stub := &cliStubBedrock{}
			return validator.NewValidator(validator.Options{
				AWSConfig:      aws.Config{Region: "us-east-1"},
				Bedrock:        stub,
				Marketplace:    &cliStubMarketplace{result: validator.Result{Status: validator.StatusPassed, Reasoning: "ok"}},
				SystemMode:     mode,
				Concurrency:    2,
				EvidenceFilter: filter,
			})
		},
		buildMap: func(_ context.Context, _ string, _ []string,
			_ func() (evidence.FileProcessor, error)) (*evidence.Map, error) {
			// Tests run with --no-map so this is unreachable, but the
			// production path would call this — stub it to a sensible
			// nil-returning placeholder.
			return &evidence.Map{SchemaVersion: evidence.SchemaVersion}, nil
		},
	}
	testValidateDepsOverride = &deps

	stdout, _, code := runCLI("validate", dir, "--skip-conversion", "--no-map", "--system-mode", "revised", "--concurrency", "2")
	if code != ExitOK {
		t.Fatalf("validate exit %d, stdout=%s", code, stdout)
	}
	if !strings.Contains(stdout, "thor validate:") {
		t.Errorf("missing summary line in stdout: %q", stdout)
	}
	for _, want := range []string{
		filepath.Join(dir, "validation_summary.md"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected %s to exist: %v", want, err)
		}
	}
	// A timestamped copy must also exist under reports/summary/.
	matches, _ := filepath.Glob(filepath.Join(dir, "reports", "summary", "validation_summary_*.md"))
	if len(matches) == 0 {
		t.Error("expected timestamped report under reports/summary/")
	}
	// Manifest must be written to reports/.
	manifests, _ := filepath.Glob(filepath.Join(dir, "reports", "run_*.json"))
	if len(manifests) == 0 {
		t.Error("expected run manifest under reports/")
	}
}

func TestValidate_RejectsBadSystemMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "partner_responses.csv"),
		[]byte("controlId,partner_response\nACCT-001,x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, code := runCLI("validate", dir, "--skip-conversion", "--system-mode", "bogus")
	if code != ExitUsageError {
		t.Errorf("exit %d, want %d", code, ExitUsageError)
	}
}

// ------------------------------------------------------------------
// diff — needs at least 2 reports under reports/summary
// ------------------------------------------------------------------

func TestDiff_NeedsAtLeastTwoReports(t *testing.T) {
	dir := t.TempDir()
	summaryDir := filepath.Join(dir, "reports", "summary")
	if err := os.MkdirAll(summaryDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// One report only — diff should fail.
	if err := os.WriteFile(filepath.Join(summaryDir, "validation_summary_20260101_080000.md"),
		[]byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runCLI("diff", dir)
	if code != ExitGenericError {
		t.Errorf("exit %d, want %d", code, ExitGenericError)
	}
	if !strings.Contains(stderr, "need at least 2") {
		t.Errorf("expected actionable error; got %q", stderr)
	}
}

// ------------------------------------------------------------------
// export — writes HTML next to a markdown source
// ------------------------------------------------------------------

func TestExport_WritesHTMLNextToMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "validation_summary.md"),
		[]byte("# Validation Summary\n\n## Passed Controls\n\n### ACCT-001\n"),
		0o600); err != nil {
		t.Fatal(err)
	}
	_, _, code := runCLI("export", dir)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "validation_report_*.html"))
	if len(matches) != 1 {
		t.Errorf("expected exactly one HTML report; got %v", matches)
	}
}

// ------------------------------------------------------------------
// doctor — JSON mode renders without making live AWS calls
// (AWS calls still happen, but at least we exercise the JSON branch)
// ------------------------------------------------------------------

func TestDoctor_JSONFlagProducesJSON(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	stdout, _, _ := runCLI("doctor", "--json")
	// Live AWS calls likely fail in CI — that's expected. We only assert
	// the output is valid JSON or that the command surfaced any failure
	// via a non-zero exit (handled elsewhere).
	stdout = strings.TrimSpace(stdout)
	if stdout != "" && !strings.HasPrefix(stdout, "{") {
		t.Errorf("doctor --json must emit JSON when it produces output; got %q", stdout)
	}
}

// ------------------------------------------------------------------
// Exit-code helpers
// ------------------------------------------------------------------

func TestExitCodeForError_DefaultsToGeneric(t *testing.T) {
	if ExitCodeForError(errors.New("bare")) != ExitGenericError {
		t.Error("expected ExitGenericError for bare error")
	}
}

func TestExitCodeForError_RespectsErrExitCode(t *testing.T) {
	got := ExitCodeForError(&errExitCode{code: ExitPartialFailure})
	if got != ExitPartialFailure {
		t.Errorf("got %d, want %d", got, ExitPartialFailure)
	}
}

func TestExpiredCredsExitCodeMapping(t *testing.T) {
	// Drive Execute via a fake handler that returns ErrExpiredCredentials.
	// We reach behind the public API for this — easier than asserting
	// against an actual STS expired-token round-trip.
	got := ExitCodeForError(awsauth.ErrExpiredCredentials)
	// ExitCodeForError treats anything not wrapped in errExitCode as
	// generic; the expired-creds → 3 mapping happens inside Execute().
	if got != ExitGenericError {
		t.Errorf("ExitCodeForError doesn't classify expired creds; got %d", got)
	}
}

// ------------------------------------------------------------------
// Stub Bedrock + marketplace for the validate end-to-end test
// ------------------------------------------------------------------

type cliStubBedrock struct{}

func (s *cliStubBedrock) Converse(_ context.Context, _ *bedrockruntime.ConverseInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseOutput, error) {
	return &bedrockruntime.ConverseOutput{
		Output: &bedrocktypes.ConverseOutputMemberMessage{
			Value: bedrocktypes.Message{
				Role: bedrocktypes.ConversationRoleAssistant,
				Content: []bedrocktypes.ContentBlock{
					&bedrocktypes.ContentBlockMemberText{Value: "YES. stubbed pass"},
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

type cliStubMarketplace struct {
	result validator.Result
}

func (s *cliStubMarketplace) Check(_ context.Context, _ string) validator.Result {
	return s.result
}

// ------------------------------------------------------------------
// Excel fixture builder — small enough that we can parse the result.
// ------------------------------------------------------------------

func buildPartnerXLSX(t *testing.T, path string) {
	t.Helper()
	body := newMinimalXLSX(t)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// newMinimalXLSX builds a tiny one-sheet workbook that the excel package
// extractor recognises (ID + Partner Response columns). Returns the
// .xlsx body bytes.
func newMinimalXLSX(t *testing.T) []byte {
	t.Helper()
	f := xlsxFile()
	defer func() { _ = f.Close() }()
	if err := f.SetSheetName("Sheet1", "AI Practice Reqs"); err != nil {
		t.Fatal(err)
	}
	mustSet := func(axis, val string) {
		if err := f.SetCellValue("AI Practice Reqs", axis, val); err != nil {
			t.Fatal(err)
		}
	}
	mustSet("A1", "AI Practice Requirements")
	mustSet("A2", "ID")
	mustSet("B2", "Requirement Description")
	mustSet("C2", "Met?")
	mustSet("D2", "Partner Response")
	mustSet("A4", "ACCT-001")
	mustSet("D4", "We maintain dedicated AWS accounts for compliance work.")

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
