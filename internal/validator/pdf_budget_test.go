package validator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	pdfcpuapi "github.com/pdfcpu/pdfcpu/pkg/api"
)

// makePDFFixture writes a PDF with `pages` real pages to outPath using
// pdfcpu's JSON-driven Create API. Mirrors the helper in internal/pdf so
// pdf.CountPages returns the expected page count.
func makePDFFixture(t *testing.T, outPath string, pages int) {
	t.Helper()

	type fontSpec struct {
		Name string `json:"name"`
		Size int    `json:"size"`
	}
	type fontRef struct {
		Name string `json:"name"`
	}
	type textRun struct {
		Value  string  `json:"value"`
		Anchor string  `json:"anchor"`
		Font   fontRef `json:"font"`
	}
	type pageSpec struct {
		Content struct {
			Text []textRun `json:"text"`
		} `json:"content"`
	}
	type doc struct {
		Paper string              `json:"paper"`
		Fonts map[string]fontSpec `json:"fonts"`
		Pages map[string]pageSpec `json:"pages"`
	}

	d := doc{
		Paper: "Letter",
		Fonts: map[string]fontSpec{
			"regular": {Name: "Helvetica", Size: 24},
		},
		Pages: make(map[string]pageSpec, pages),
	}
	for i := 1; i <= pages; i++ {
		var p pageSpec
		p.Content.Text = []textRun{{
			Value:  fmt.Sprintf("Page %d", i),
			Anchor: "center",
			Font:   fontRef{Name: "$regular"},
		}}
		d.Pages[fmt.Sprintf("%d", i)] = p
	}
	jsonBytes, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}

	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()

	if err := pdfcpuapi.Create(nil, bytes.NewReader(jsonBytes), out, nil); err != nil {
		t.Fatalf("pdfcpu Create failed for %d-page fixture: %v", pages, err)
	}
}

func newBudgetTestValidator(t *testing.T) *Validator {
	t.Helper()
	v := &Validator{
		logger:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		imageCache: newImageCache(),
		counters:   &Counters{},
	}
	return v
}

// Under-budget: total pages fit within MaxPDFPagesPerCall, no demotions.
func TestPlanPDFDemotions_UnderBudgetIsNoop(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.pdf")
	b := filepath.Join(dir, "b.pdf")
	makePDFFixture(t, a, 10)
	makePDFFixture(t, b, 20)

	v := newBudgetTestValidator(t)
	got := v.planPDFDemotions([]string{a, b}, 95)
	if len(got) != 0 {
		t.Errorf("under-budget should yield no demotions, got %v", got)
	}
}

// Over-budget: largest PDF demotes first; smaller PDFs survive as binary.
func TestPlanPDFDemotions_DemotesLargestFirst(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.pdf")
	medium := filepath.Join(dir, "medium.pdf")
	large := filepath.Join(dir, "large.pdf")
	makePDFFixture(t, small, 14)
	makePDFFixture(t, medium, 44)
	makePDFFixture(t, large, 62)

	// 14 + 44 + 62 = 120 > 95. Demoting the 62-page deck leaves 58, fits.
	v := newBudgetTestValidator(t)
	got := v.planPDFDemotions([]string{small, medium, large}, 95)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 demotion (largest), got %d: %v", len(got), got)
	}
	if !got[large] {
		t.Errorf("largest PDF (62 pages) should be demoted; got demotion set %v", got)
	}
	if got[small] || got[medium] {
		t.Errorf("smaller PDFs should not be demoted; got %v", got)
	}
}

// Tiny budget forces demoting all PDFs.
func TestPlanPDFDemotions_TightBudgetDemotesAll(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.pdf")
	b := filepath.Join(dir, "b.pdf")
	makePDFFixture(t, a, 10)
	makePDFFixture(t, b, 20)

	v := newBudgetTestValidator(t)
	got := v.planPDFDemotions([]string{a, b}, 5) // 5-page budget
	if len(got) != 2 {
		t.Errorf("budget too tight, both PDFs should demote, got %v", got)
	}
}

// processFiles end-to-end: when budget exceeded, the demoted PDFs come
// back as txt-format document blocks and the counter ticks up.
func TestProcessFiles_DemotesOversizePDFsToTxtBlocks(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.pdf")
	large := filepath.Join(dir, "large.pdf")
	makePDFFixture(t, small, 30)
	makePDFFixture(t, large, 80) // 30 + 80 = 110 > 95

	v := newBudgetTestValidator(t)
	blocks, err := v.processFiles([]string{small, large})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 content blocks, got %d", len(blocks))
	}
	if v.counters.DemotedPDFs != 1 {
		t.Errorf("DemotedPDFs counter = %d, want 1", v.counters.DemotedPDFs)
	}
}
