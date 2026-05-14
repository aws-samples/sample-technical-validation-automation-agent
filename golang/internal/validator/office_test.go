package validator

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makePPTX builds a minimal .pptx zip with N slides, each carrying the
// supplied per-slide text inside <a:t> elements. Slides go into the zip
// in shuffled order to verify the reader sorts them by index.
func makePPTX(t *testing.T, path string, slides []string, shuffle bool) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// minimal package parts pdfcpu/openxml don't actually require for
	// our reader — we only inspect ppt/slides/slide*.xml.
	order := make([]int, len(slides))
	for i := range order {
		order[i] = i + 1
	}
	if shuffle && len(order) >= 2 {
		// Reverse so slide order is wrong-by-default in zip iteration.
		for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
			order[i], order[j] = order[j], order[i]
		}
	}
	for _, idx := range order {
		f, err := zw.Create(fmt.Sprintf("ppt/slides/slide%d.xml", idx))
		if err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`<?xml version="1.0"?>
<sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <a:t>%s</a:t>
  <a:t></a:t>
  <a:t>   </a:t>
</sld>`, slides[idx-1])
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExtractPPTXText_OrdersBySlideNumber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deck.pptx")
	makePPTX(t, path, []string{"alpha", "beta", "gamma"}, true /* shuffle */)

	got, err := ExtractPPTXText(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "alpha\nbeta\ngamma"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractPPTXText_RespectsByteCap_B4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deck.pptx")
	makePPTX(t, path, []string{"hello", "world"}, false)

	got, err := ExtractPPTXText(path, 7) // can fit "hello\nw" but should stop on rune boundary
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 7 {
		t.Errorf("got %d bytes (%q), want <= 7", len(got), got)
	}
}

func TestExtractPPTXText_NoSlidesReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deck.pptx")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_, _ = zw.Create("docProps/core.xml")
	_ = zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ExtractPPTXText(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty for slide-less pptx", got)
	}
}

// makeDOCX writes a minimal .docx zip with the given runs in word/document.xml.
func makeDOCX(t *testing.T, path string, runs []string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	f, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	body := `<?xml version="1.0"?>
<document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>`
	for _, r := range runs {
		body += fmt.Sprintf(`<w:p><w:r><w:t>%s</w:t></w:r></w:p>`, r)
	}
	body += `</w:body></document>`
	if _, err := f.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExtractDOCXText_ConcatenatesRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.docx")
	makeDOCX(t, path, []string{"first paragraph", "second paragraph"})

	got, err := ExtractDOCXText(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "first paragraph") || !strings.Contains(got, "second paragraph") {
		t.Errorf("missing expected runs in %q", got)
	}
}

func TestExtractDOCXText_MissingDocumentXMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.docx")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_, _ = zw.Create("docProps/core.xml")
	_ = zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ExtractDOCXText(path, 0); err == nil {
		t.Error("expected error for docx missing word/document.xml")
	}
}

func TestSlideNumber(t *testing.T) {
	cases := map[string]struct {
		n  int
		ok bool
	}{
		"ppt/slides/slide1.xml":   {1, true},
		"ppt/slides/slide12.xml":  {12, true},
		"ppt/slides/slide.xml":    {0, false},
		"ppt/slides/slideA.xml":   {0, false},
		"ppt/slides/_rels/x.rels": {0, false},
		"docProps/core.xml":       {0, false},
	}
	for in, want := range cases {
		got, ok := slideNumber(in)
		if got != want.n || ok != want.ok {
			t.Errorf("slideNumber(%q) = (%d, %v), want (%d, %v)", in, got, ok, want.n, want.ok)
		}
	}
}
