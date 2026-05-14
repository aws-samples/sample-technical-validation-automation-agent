package excel

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// buildFixture writes a workbook that mirrors the structure of a real
// partner submission (modeled after the Metal_Toad sample) but with dummy
// data. Tests use this as a stable golden fixture without committing
// partner content.
//
// Layout matches Metal_Toad:
//   - "Introduction"               — text-only, must be skipped.
//   - "AI Practice Reqs"           — single-response, header row 1, "Partner Response" in header.
//   - "Common Practice Requirements" — single-response with a section header in row 2.
//   - "AI Cust Example Reqs"       — 9-col header row 1, "Partner Response" in row 2 → falls into single-response path with multiple matching columns.
//   - "Common Cust Example Reqs"   — full 11-col layout, must hit the hardcoded E/G/I/K (4/6/8/10) branch.
//   - "Misc Notes"                 — non-data sheet with no "ID" cell, must be skipped silently.
func buildFixture(t *testing.T, path string) {
	t.Helper()

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	// excelize creates Sheet1 by default; rename it to Introduction so we
	// don't have a stray empty sheet.
	if err := f.SetSheetName("Sheet1", "Introduction"); err != nil {
		t.Fatal(err)
	}
	mustSet(t, f, "Introduction", "A1", "AWS AI Competency Service Offering Validation Checklist")

	// --- AI Practice Reqs ---------------------------------------------
	mustNew(t, f, "AI Practice Reqs")
	mustSet(t, f, "AI Practice Reqs", "A1", "AI Practice Requirements")
	mustSet(t, f, "AI Practice Reqs", "A2", "ID")
	mustSet(t, f, "AI Practice Reqs", "B2", "Requirement Description")
	mustSet(t, f, "AI Practice Reqs", "C2", "Met?")
	mustSet(t, f, "AI Practice Reqs", "D2", "Partner Response")
	// Section header row mimicking Metal_Toad row 2.
	mustSet(t, f, "AI Practice Reqs", "A3", "Generative AI Practice Requirements")
	// Data rows.
	mustSet(t, f, "AI Practice Reqs", "A5", "GENAIPR-001")
	mustSet(t, f, "AI Practice Reqs", "B5", "Customer onboarding strategy")
	mustSet(t, f, "AI Practice Reqs", "C5", "Yes")
	mustSet(t, f, "AI Practice Reqs", "D5", "We onboard customers via a 4-stage program.")
	mustSet(t, f, "AI Practice Reqs", "A6", "GENAIPR-002")
	mustSet(t, f, "AI Practice Reqs", "B6", "FM selection")
	mustSet(t, f, "AI Practice Reqs", "C6", "Yes")
	mustSet(t, f, "AI Practice Reqs", "D6", "Methodical evidence-based approach to FM selection.")
	// Empty response should be skipped.
	mustSet(t, f, "AI Practice Reqs", "A7", "GENAIPR-003")
	mustSet(t, f, "AI Practice Reqs", "B7", "Adoption metrics")
	mustSet(t, f, "AI Practice Reqs", "C7", "No")
	mustSet(t, f, "AI Practice Reqs", "D7", "")

	// --- Common Practice Requirements ---------------------------------
	mustNew(t, f, "Common Practice Requirements")
	mustSet(t, f, "Common Practice Requirements", "A1", "Common AWS Partner Practice Requirements")
	mustSet(t, f, "Common Practice Requirements", "A2", "ID")
	mustSet(t, f, "Common Practice Requirements", "B2", "Requirement Description")
	mustSet(t, f, "Common Practice Requirements", "C2", "Met?")
	mustSet(t, f, "Common Practice Requirements", "D2", "Partner Response")
	mustSet(t, f, "Common Practice Requirements", "A3", "AI Practice Overview")
	mustSet(t, f, "Common Practice Requirements", "A5", "POV-001")
	mustSet(t, f, "Common Practice Requirements", "D5", "Slide deck in drive")
	// nan-literal response must be filtered (B28).
	mustSet(t, f, "Common Practice Requirements", "A6", "POV-002")
	mustSet(t, f, "Common Practice Requirements", "D6", "nan")
	// Suffix-mapped control: DOC-001 → DOC-001-SOFTWARE for AppTypeSoftware.
	mustSet(t, f, "Common Practice Requirements", "A7", "DOC-001")
	mustSet(t, f, "Common Practice Requirements", "D7", "Documented architecture review.")

	// --- AI Cust Example Reqs -----------------------------------------
	// Header row at row 1, second header row (row 2) carries the "Partner
	// Response" labels — should hit the dynamic single-response branch and
	// produce four response columns.
	mustNew(t, f, "AI Cust Example Reqs")
	mustSet(t, f, "AI Cust Example Reqs", "A1", "AI Customer Example Requirements")
	mustSet(t, f, "AI Cust Example Reqs", "A2", "ID")
	mustSet(t, f, "AI Cust Example Reqs", "B2", "Requirement Description")
	mustSet(t, f, "AI Cust Example Reqs", "C2", "Customer Reference #1")
	mustSet(t, f, "AI Cust Example Reqs", "E2", "Customer Reference #2")
	mustSet(t, f, "AI Cust Example Reqs", "G2", "Customer Reference #3")
	mustSet(t, f, "AI Cust Example Reqs", "I2", "Customer Reference #4")
	mustSet(t, f, "AI Cust Example Reqs", "C3", "Met?")
	mustSet(t, f, "AI Cust Example Reqs", "D3", "Partner Response")
	mustSet(t, f, "AI Cust Example Reqs", "E3", "Met?")
	mustSet(t, f, "AI Cust Example Reqs", "F3", "Partner Response")
	mustSet(t, f, "AI Cust Example Reqs", "G3", "Met?")
	mustSet(t, f, "AI Cust Example Reqs", "H3", "Partner Response")
	mustSet(t, f, "AI Cust Example Reqs", "I3", "Met?")
	mustSet(t, f, "AI Cust Example Reqs", "J3", "Partner Response")
	// Data: customer-example sheets skip an extra row, so data starts at row 5.
	mustSet(t, f, "AI Cust Example Reqs", "A5", "GENAICEX-001")
	mustSet(t, f, "AI Cust Example Reqs", "D5", "Cust1 narrative")
	mustSet(t, f, "AI Cust Example Reqs", "F5", "Cust2 narrative")
	mustSet(t, f, "AI Cust Example Reqs", "H5", "Cust3 narrative")
	mustSet(t, f, "AI Cust Example Reqs", "J5", "Cust4 narrative")

	// --- Common Cust Example Reqs -------------------------------------
	// Hardcoded E/G/I/K (cols 4/6/8/10). Header row contains "ID" at A2.
	mustNew(t, f, "Common Cust Example Reqs")
	mustSet(t, f, "Common Cust Example Reqs", "A1", "Common Customer Example Requirements")
	mustSet(t, f, "Common Cust Example Reqs", "A2", "ID")
	mustSet(t, f, "Common Cust Example Reqs", "B2", "Requirement Description")
	mustSet(t, f, "Common Cust Example Reqs", "C2", "Example Response")
	mustSet(t, f, "Common Cust Example Reqs", "D2", "Customer Reference #1")
	mustSet(t, f, "Common Cust Example Reqs", "F2", "Customer Reference #2")
	mustSet(t, f, "Common Cust Example Reqs", "H2", "Customer Reference #3")
	mustSet(t, f, "Common Cust Example Reqs", "J2", "Customer Reference #4")
	mustSet(t, f, "Common Cust Example Reqs", "D3", "Met?")
	mustSet(t, f, "Common Cust Example Reqs", "E3", "Partner Response")
	mustSet(t, f, "Common Cust Example Reqs", "F3", "Met?")
	mustSet(t, f, "Common Cust Example Reqs", "G3", "Partner Response")
	mustSet(t, f, "Common Cust Example Reqs", "H3", "Met?")
	mustSet(t, f, "Common Cust Example Reqs", "I3", "Partner Response")
	mustSet(t, f, "Common Cust Example Reqs", "J3", "Met?")
	mustSet(t, f, "Common Cust Example Reqs", "K3", "Partner Response")
	mustSet(t, f, "Common Cust Example Reqs", "A4", "Use Case Relevance")
	// Data — USE-CASE row populated for all four customer columns.
	mustSet(t, f, "Common Cust Example Reqs", "A5", "USE-CASE")
	mustSet(t, f, "Common Cust Example Reqs", "E5", "Cust1 use case")
	mustSet(t, f, "Common Cust Example Reqs", "G5", "Cust2 use case")
	mustSet(t, f, "Common Cust Example Reqs", "I5", "Cust3 use case")
	mustSet(t, f, "Common Cust Example Reqs", "K5", "Cust4 use case")

	// --- Misc Notes (no ID column) — must be skipped silently ---------
	mustNew(t, f, "Misc Notes")
	mustSet(t, f, "Misc Notes", "A1", "freeform notes")
	mustSet(t, f, "Misc Notes", "A2", "no header here")

	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

func mustNew(t *testing.T, f *excelize.File, sheet string) {
	t.Helper()
	if _, err := f.NewSheet(sheet); err != nil {
		t.Fatalf("NewSheet(%q): %v", sheet, err)
	}
}

func mustSet(t *testing.T, f *excelize.File, sheet, axis, value string) {
	t.Helper()
	if err := f.SetCellValue(sheet, axis, value); err != nil {
		t.Fatalf("SetCellValue(%q, %q): %v", sheet, axis, err)
	}
}

// TestExtractResponses_GoldenFixture builds a workbook modeled on the
// Metal_Toad sample and asserts every extraction-time decision the Python
// port mandates.
func TestExtractResponses_GoldenFixture(t *testing.T) {
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "fixture.xlsx")
	csv := filepath.Join(dir, "partner_responses.csv")

	buildFixture(t, xlsx)

	if err := ExtractResponses(xlsx, csv, AppTypeSoftware, nil); err != nil {
		t.Fatal(err)
	}

	want := []Response{
		{ControlID: "GENAIPR-001", PartnerResponse: "We onboard customers via a 4-stage program."},
		{ControlID: "GENAIPR-002", PartnerResponse: "Methodical evidence-based approach to FM selection."},
		// GENAIPR-003: empty response is dropped.
		{ControlID: "POV-001", PartnerResponse: "Slide deck in drive"},
		// POV-002: "nan" literal is dropped.
		{ControlID: "DOC-001-SOFTWARE", PartnerResponse: "Documented architecture review."},
		// AI Cust Example Reqs single-response fallback finds Partner Response in row 3.
		{ControlID: "GENAICEX-001", PartnerResponse: "Cust1 narrative"},
		{ControlID: "GENAICEX-001", PartnerResponse: "Cust2 narrative"},
		{ControlID: "GENAICEX-001", PartnerResponse: "Cust3 narrative"},
		{ControlID: "GENAICEX-001", PartnerResponse: "Cust4 narrative"},
		// Common Cust Example Reqs — hardcoded E/G/I/K, suffix-mapped USE-CASE.
		{ControlID: "USE-CASE-SOFTWARE", PartnerResponse: "Cust1 use case"},
		{ControlID: "USE-CASE-SOFTWARE", PartnerResponse: "Cust2 use case"},
		{ControlID: "USE-CASE-SOFTWARE", PartnerResponse: "Cust3 use case"},
		{ControlID: "USE-CASE-SOFTWARE", PartnerResponse: "Cust4 use case"},
	}

	got, err := LoadResponses(csv)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractResponses mismatch.\n got: %s\nwant: %s",
			renderRows(got), renderRows(want))
	}
}

// TestExtractResponses_AppTypeService verifies suffix mapping uses SERVICE
// when AppTypeService is selected.
func TestExtractResponses_AppTypeService(t *testing.T) {
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "fixture.xlsx")
	csv := filepath.Join(dir, "partner_responses.csv")
	buildFixture(t, xlsx)

	if err := ExtractResponses(xlsx, csv, AppTypeService, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadResponses(csv)
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range rows {
		if strings.HasPrefix(r.ControlID, "DOC-001") && r.ControlID != "DOC-001-SERVICE" {
			t.Errorf("expected DOC-001 → DOC-001-SERVICE, got %s", r.ControlID)
		}
		if strings.HasPrefix(r.ControlID, "USE-CASE") && r.ControlID != "USE-CASE-SERVICE" {
			t.Errorf("expected USE-CASE → USE-CASE-SERVICE, got %s", r.ControlID)
		}
	}
}

// TestExtractResponses_ControlListFilter exercises both an exact suffixed
// match and a base-control alias match against the controlList allowlist.
func TestExtractResponses_ControlListFilter(t *testing.T) {
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "fixture.xlsx")
	csv := filepath.Join(dir, "partner_responses.csv")
	buildFixture(t, xlsx)

	// Allowlist contains a suffixed form (DOC-001-SOFTWARE) and a
	// non-suffixed form (POV-001). Output IDs must follow the canonical
	// suffix rules regardless of how the user specified them.
	list := []string{"DOC-001-SOFTWARE", "POV-001"}
	if err := ExtractResponses(xlsx, csv, AppTypeSoftware, list); err != nil {
		t.Fatal(err)
	}
	rows, err := LoadResponses(csv)
	if err != nil {
		t.Fatal(err)
	}

	want := []Response{
		{ControlID: "POV-001", PartnerResponse: "Slide deck in drive"},
		{ControlID: "DOC-001-SOFTWARE", PartnerResponse: "Documented architecture review."},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("controlList filter mismatch.\n got: %s\nwant: %s",
			renderRows(rows), renderRows(want))
	}
}

// TestExtractResponses_RejectsInvalidAppType
func TestExtractResponses_RejectsInvalidAppType(t *testing.T) {
	dir := t.TempDir()
	xlsx := filepath.Join(dir, "fixture.xlsx")
	csv := filepath.Join(dir, "partner_responses.csv")
	buildFixture(t, xlsx)

	if err := ExtractResponses(xlsx, csv, AppType("BOTH"), nil); err == nil {
		t.Fatal("want error for invalid app type")
	}
}

// TestLoadResponses_AcceptsBothHeaderForms verifies B2 on the read path.
func TestLoadResponses_AcceptsBothHeaderForms(t *testing.T) {
	dir := t.TempDir()
	for _, header := range []string{"controlId,partner_response", "control_id,partner_response"} {
		path := filepath.Join(dir, strings.ReplaceAll(header, ",", "_")+".csv")
		body := header + "\nACCT-001,hello\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadResponses(path)
		if err != nil {
			t.Fatalf("header %q: %v", header, err)
		}
		want := []Response{{ControlID: "ACCT-001", PartnerResponse: "hello"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("header %q: got %v, want %v", header, got, want)
		}
	}
}

func TestLoadResponses_RejectsMissingControlIDColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.csv")
	if err := os.WriteFile(path, []byte("foo,partner_response\nx,y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResponses(path); err == nil {
		t.Fatal("want error for missing controlId column")
	}
}

// renderRows formats rows for compact diff output on test failure.
func renderRows(rows []Response) string {
	var b strings.Builder
	b.WriteString("[")
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\n  ")
		}
		b.WriteString(r.ControlID)
		b.WriteString(" -> ")
		b.WriteString(r.PartnerResponse)
	}
	b.WriteString("]")
	return b.String()
}
