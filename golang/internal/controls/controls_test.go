package controls

import (
	"slices"
	"strings"
	"testing"
)

// ------------------------------------------------------------------
// Suffix helpers
// ------------------------------------------------------------------

func TestStripSuffix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"OPE-001-SERVICE", "OPE-001"},
		{"OPE-001-SOFTWARE", "OPE-001"},
		{"DOC-002-CUSTOMER-DEPLOY", "DOC-002-CUSTOMER-DEPLOY"}, // not a SERVICE/SOFTWARE suffix
		{"ACCT-001", "ACCT-001"},                               // no suffix
		{"USE-CASE-SOFTWARE", "USE-CASE"},
	}
	for _, tc := range cases {
		if got := stripSuffix(tc.in); got != tc.want {
			t.Errorf("stripSuffix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsSuffixControl(t *testing.T) {
	if !IsSuffixControl("DOC-001") {
		t.Error("DOC-001 should be in SuffixControls")
	}
	if IsSuffixControl("ACCT-001") {
		t.Error("ACCT-001 should NOT be in SuffixControls")
	}
}

// ------------------------------------------------------------------
// GetApplicable — covers audit fix B1 (returns []string only) plus one
// fixture per category in CATEGORY_CONTROLS.
// ------------------------------------------------------------------

func TestGetApplicable_GenAIApplications(t *testing.T) {
	all := []string{
		"GAIAPP-001-SOFTWARE",
		"GAIFMS-001-SOFTWARE", // wrong category — must drop
		"GAIDEV-001-SOFTWARE", // wrong category — must drop
		"DOC-001-SOFTWARE",    // common control — keep
		"ACCT-001",            // common control — keep
	}
	got := GetApplicable("Generative AI Applications", all)
	want := []string{
		"GAIAPP-001-SOFTWARE",
		"DOC-001-SOFTWARE",
		"ACCT-001",
	}
	if !slices.Equal(got, want) {
		t.Errorf("GetApplicable(GenAI Apps) = %v, want %v", got, want)
	}
}

func TestGetApplicable_FoundationModels(t *testing.T) {
	all := []string{"GAIFMS-001-SOFTWARE", "GAIDEV-001-SOFTWARE", "GAIAPP-001-SOFTWARE", "ACCT-001"}
	got := GetApplicable("Foundation Models and App Development", all)
	want := []string{"GAIFMS-001-SOFTWARE", "GAIDEV-001-SOFTWARE", "ACCT-001"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetApplicable_InfrastructureAndData(t *testing.T) {
	all := []string{"GAIPBHW-001", "GAIDSI-001", "GAISDG-001", "GAIAPP-001-SOFTWARE", "ACCT-001"}
	got := GetApplicable("Infrastructure and Data", all)
	want := []string{"GAIPBHW-001", "GAIDSI-001", "GAISDG-001", "ACCT-001"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetApplicable_AgenticTools(t *testing.T) {
	all := []string{"QCHKT-001", "QCHKA-001", "QCHK-001", "ACCT-001"}
	got := GetApplicable("Agentic AI Tools", all)
	want := []string{"QCHKT-001", "ACCT-001"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetApplicable_AgenticApplications(t *testing.T) {
	all := []string{"QCHKA-001", "QCHKT-001", "ACCT-001"}
	got := GetApplicable("Agentic AI Applications", all)
	want := []string{"QCHKA-001", "ACCT-001"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetApplicable_AgenticConsulting(t *testing.T) {
	all := []string{"QCHK-001", "AGAIPS-001", "QCHKT-001", "ACCT-001"}
	got := GetApplicable("Agentic AI Consulting Services", all)
	want := []string{"QCHK-001", "AGAIPS-001", "ACCT-001"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetApplicable_GenAIConsulting(t *testing.T) {
	all := []string{"GENAICEX-001", "GENAIPR-001", "GAIAPP-001-SOFTWARE", "ACCT-001"}
	got := GetApplicable("Generative AI Consulting Services", all)
	want := []string{"GENAICEX-001", "GENAIPR-001", "ACCT-001"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetApplicable_EmptyCategory_ReturnsAll(t *testing.T) {
	all := []string{"GAIAPP-001-SOFTWARE", "ACCT-001"}
	got := GetApplicable("", all)
	if !slices.Equal(got, all) {
		t.Errorf("empty category: got %v, want %v", got, all)
	}
}

func TestGetApplicable_UnknownCategory_ReturnsAll(t *testing.T) {
	all := []string{"GAIAPP-001-SOFTWARE", "ACCT-001"}
	got := GetApplicable("Made-Up Category", all)
	if !slices.Equal(got, all) {
		t.Errorf("unknown category: got %v, want %v", got, all)
	}
}

// B1: function returns []string only — verify by signature alone via
// compile-time assignment.
func TestGetApplicable_ReturnsFlatSlice_B1(t *testing.T) {
	got := GetApplicable("Generative AI Applications", []string{"ACCT-001"})
	if len(got) != 1 || got[0] != "ACCT-001" {
		t.Errorf("got %v, want [ACCT-001]", got)
	}
}

// ------------------------------------------------------------------
// Load — embedded CONTEXT.csv parses cleanly.
// ------------------------------------------------------------------

func TestLoad_ParsesEmbeddedCSV(t *testing.T) {
	rows, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("Load() returned no rows")
	}
	for _, r := range rows {
		if r.ID == "" {
			t.Errorf("empty control ID in CONTEXT.csv: %+v", r)
		}
		if r.PromptContext == "" {
			t.Errorf("empty prompt_context for %s", r.ID)
		}
	}
}

func TestLoadMap_RejectsDuplicates(t *testing.T) {
	rows, err := LoadMap()
	if err != nil {
		t.Fatalf("LoadMap(): %v", err)
	}
	if _, ok := rows["ACCT-001"]; !ok {
		t.Error("expected ACCT-001 in LoadMap()")
	}
}

// parseContextCSV accepts either header form (audit fix B2).
func TestParseContextCSV_AcceptsControlIDAlias(t *testing.T) {
	for _, header := range []string{"controlId,prompt_context", "control_id,prompt_context"} {
		csv := header + "\nACCT-001,hello\n"
		rows, err := parseContextCSV(strings.NewReader(csv))
		if err != nil {
			t.Fatalf("header %q: %v", header, err)
		}
		if len(rows) != 1 || rows[0].ID != "ACCT-001" {
			t.Errorf("header %q: got %+v", header, rows)
		}
	}
}

func TestParseContextCSV_MissingControlIDColumn(t *testing.T) {
	csv := "foo,prompt_context\nx,y\n"
	if _, err := parseContextCSV(strings.NewReader(csv)); err == nil {
		t.Fatal("want error for missing controlId column, got nil")
	}
}

func TestCanonicalHeader(t *testing.T) {
	cases := map[string]string{
		"controlId":        "controlId",
		"control_id":       "controlId",
		"  control_id  ":   "controlId",
		"partner_response": "partner_response",
		"weird":            "weird",
	}
	for in, want := range cases {
		if got := CanonicalHeader(in); got != want {
			t.Errorf("CanonicalHeader(%q) = %q, want %q", in, got, want)
		}
	}
}

// ------------------------------------------------------------------
// SuffixDriftWarnings — B17b
// ------------------------------------------------------------------

func TestSuffixDriftWarnings_FlagsMissingSuffix(t *testing.T) {
	context := []string{"OPE-001-SOFTWARE", "ACCT-001"}
	partner := []string{"OPE-001", "ACCT-001"}

	got := SuffixDriftWarnings(context, partner)
	if len(got) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "OPE-001-SOFTWARE") {
		t.Errorf("warning missing control ID: %q", got[0])
	}
}

func TestSuffixDriftWarnings_NoFalsePositiveWhenSuffixMatches(t *testing.T) {
	context := []string{"OPE-001-SOFTWARE", "ACCT-001"}
	partner := []string{"OPE-001-SOFTWARE", "ACCT-001"}

	if got := SuffixDriftWarnings(context, partner); len(got) != 0 {
		t.Errorf("expected no warnings, got %v", got)
	}
}

func TestSuffixDriftWarnings_NoFalsePositiveWhenPartnerSkipsControl(t *testing.T) {
	context := []string{"OPE-001-SOFTWARE", "ACCT-001"}
	partner := []string{"ACCT-001"} // partner did not submit OPE-001 at all

	if got := SuffixDriftWarnings(context, partner); len(got) != 0 {
		t.Errorf("expected no warnings (partner skipped control), got %v", got)
	}
}

func TestSuffixDriftWarnings_IgnoresNonSuffixedContextEntries(t *testing.T) {
	context := []string{"ACCT-001"}
	partner := []string{"ACCT-001"}

	if got := SuffixDriftWarnings(context, partner); len(got) != 0 {
		t.Errorf("expected no warnings, got %v", got)
	}
}
