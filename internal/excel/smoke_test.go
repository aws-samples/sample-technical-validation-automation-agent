package excel

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSmoke_RealPartnerWorkbook is an opt-in sanity check: if a real
// partner workbook is present at the maintainer-known sample path, run
// extraction against it and assert (a) the parser does not error, (b) it
// produces a non-empty CSV, and (c) every emitted control ID is
// non-empty.
//
// The sample path is deliberately outside the repo (`../sample/...`); the
// test silently skips when the file is absent so CI and contributor
// machines without partner data stay green.
func TestSmoke_RealPartnerWorkbook(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	candidates := []string{
		filepath.Join(wd, "..", "..", "..", "sample", "Metal_Toad_AWS AI Competency Self-Assessment.xlsx"),
	}
	var xlsx string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			xlsx = p
			break
		}
	}
	if xlsx == "" {
		t.Skip("no real partner sample present; skipping smoke test")
	}

	dir := t.TempDir()
	csv := filepath.Join(dir, "partner_responses.csv")

	if err := ExtractResponses(xlsx, csv, AppTypeSoftware, nil); err != nil {
		t.Fatalf("ExtractResponses against real sample: %v", err)
	}

	rows, err := LoadResponses(csv)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatalf("real sample produced 0 responses; expected many")
	}
	for _, r := range rows {
		if r.ControlID == "" {
			t.Errorf("empty control ID in extracted row: %+v", r)
		}
	}
	t.Logf("smoke test extracted %d responses from %s", len(rows), filepath.Base(xlsx))
}
