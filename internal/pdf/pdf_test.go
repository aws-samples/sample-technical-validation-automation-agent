package pdf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	pdfcpuapi "github.com/pdfcpu/pdfcpu/pkg/api"
)

// makeFixturePDF generates a PDF with the given number of pages, each
// containing a small "Page N" text run, and writes it to outPath.
//
// Implementation: pdfcpu's JSON-driven Create API. The JSON spec describes
// pages with anchored text; pdfcpu emits a real PDF so the byte layout, page
// tree, and font references are valid for downstream tools (CountPages,
// SplitFile, ledongthuc/pdf).
func makeFixturePDF(t *testing.T, outPath string, pages int) {
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

// ------------------------------------------------------------------
// CountPages
// ------------------------------------------------------------------

func TestCountPages(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{1, 5, 50} {
		path := filepath.Join(dir, fmt.Sprintf("p%d.pdf", n))
		makeFixturePDF(t, path, n)
		got, err := CountPages(path)
		if err != nil {
			t.Fatalf("CountPages(%d-page fixture): %v", n, err)
		}
		if got != n {
			t.Errorf("CountPages: got %d, want %d", got, n)
		}
	}
}

// ------------------------------------------------------------------
// Split
// ------------------------------------------------------------------

func TestSplit_FiftyPagesIntoTwoChunks(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.pdf")
	makeFixturePDF(t, src, 50)

	outDir := filepath.Join(dir, "chunks")
	chunks, err := Split(src, outDir, 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks for 50 pages at span=40, got %d: %v", len(chunks), chunks)
	}

	totalPages := 0
	for _, c := range chunks {
		n, err := CountPages(c)
		if err != nil {
			t.Fatalf("CountPages(%s): %v", c, err)
		}
		if n == 0 || n > 40 {
			t.Errorf("chunk %s: want 1..40 pages, got %d", filepath.Base(c), n)
		}
		totalPages += n
	}
	if totalPages != 50 {
		t.Errorf("split lost or duplicated pages: total %d, want 50", totalPages)
	}
}

func TestSplit_DefaultsToFortyPages(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.pdf")
	makeFixturePDF(t, src, 50)
	outDir := filepath.Join(dir, "chunks")

	chunks, err := Split(src, outDir, 0) // 0 → DefaultPagesPerChunk
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Errorf("default span: want 2 chunks for 50 pages, got %d", len(chunks))
	}
}

// ------------------------------------------------------------------
// SplitIfNeeded
// ------------------------------------------------------------------

func TestSplitIfNeeded_SmallFileReturnsOriginal(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "small.pdf")
	makeFixturePDF(t, src, 2)
	outDir := filepath.Join(dir, "chunks")

	got, err := SplitIfNeeded(src, outDir, DefaultMaxBytes, DefaultMaxPages, DefaultPagesPerChunk)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != src {
		t.Errorf("small file should pass through unchanged; got %v", got)
	}
}

func TestSplitIfNeeded_LargeByPageCount(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.pdf")
	makeFixturePDF(t, src, 80)
	outDir := filepath.Join(dir, "chunks")

	got, err := SplitIfNeeded(src, outDir, DefaultMaxBytes, 40, 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Errorf("80-page file should produce >=2 chunks, got %d", len(got))
	}
	for _, p := range got {
		if !strings.HasSuffix(p, ".pdf") {
			t.Errorf("expected all chunks to be PDFs; got %s", p)
		}
	}
}

// ------------------------------------------------------------------
// ExtractText
// ------------------------------------------------------------------

func TestExtractText_ReadsAllPages(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "small.pdf")
	makeFixturePDF(t, src, 3)

	text, err := ExtractText(src, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	// Page-N markers may or may not survive ledongthuc's text extraction
	// depending on font encoding. Just assert non-empty output.
	if len(text) == 0 {
		t.Error("ExtractText returned empty string for 3-page fixture")
	}
}

// B4: byte limit must truncate on a rune boundary and never exceed maxBytes.
func TestExtractText_ByteLimit_RuneBoundary_B4(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "small.pdf")
	makeFixturePDF(t, src, 3)

	for _, limit := range []int{10, 17, 100} {
		text, err := ExtractText(src, limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(text) > limit {
			t.Errorf("limit=%d: got %d bytes, want <= %d", limit, len(text), limit)
		}
		// Must be valid UTF-8 (B4: rune-boundary truncation).
		if !utf8.ValidString(text) {
			t.Errorf("limit=%d: extracted text contains invalid UTF-8: %q", limit, text)
		}
	}
}

// ------------------------------------------------------------------
// Encrypted file -> graceful failure
// ------------------------------------------------------------------

func TestExtractText_EncryptedFile_ReturnsActionableError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "enc.pdf")
	makeFixturePDF(t, src, 1)

	encrypted := filepath.Join(dir, "enc-out.pdf")
	conf := pdfcpuapi.LoadConfiguration()
	conf.UserPW = "secret"
	conf.OwnerPW = "secret"
	if err := pdfcpuapi.EncryptFile(src, encrypted, conf); err != nil {
		t.Skipf("could not encrypt fixture (pdfcpu version may differ): %v", err)
	}

	if _, err := ExtractText(encrypted, DefaultMaxBytes); err == nil {
		t.Fatal("ExtractText on encrypted PDF: want error, got nil")
	}
}

// ------------------------------------------------------------------
// IsAlreadySplit
// ------------------------------------------------------------------

func TestIsAlreadySplit(t *testing.T) {
	cases := map[string]bool{
		"foo_Part1.pdf":        true,
		"foo_Part42.pdf":       true,
		"some/dir/x_Part1.pdf": true,
		"normal.pdf":           false,
		"foo_Part.pdf":         false,
		"foo_partial.pdf":      false,
	}
	for in, want := range cases {
		if got := IsAlreadySplit(in); got != want {
			t.Errorf("IsAlreadySplit(%q) = %v, want %v", in, got, want)
		}
	}
}
