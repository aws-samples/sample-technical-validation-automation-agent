package validator

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

// ExtractPPTXText concatenates every <a:t> text run from every slide in a
// .pptx file, in slide-number order, separated by newlines. Pure stdlib
// (archive/zip + encoding/xml) — no third-party Office dependency.
//
// Audit improvement (PLAN.md decision #17): the Go port handles every
// .pptx, regardless of size. The Python implementation only does this on
// the oversize fallback path, so small .pptx files are silently dropped.
//
// Truncation honours maxBytes (B4): UTF-8 boundaries are respected, output
// never exceeds the cap.
func ExtractPPTXText(path string, maxBytes int) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("validator: open pptx %s: %w", path, err)
	}
	defer func() { _ = zr.Close() }()

	type slideEntry struct {
		num int
		f   *zip.File
	}

	var slides []slideEntry
	for _, f := range zr.File {
		n, ok := slideNumber(f.Name)
		if !ok {
			continue
		}
		slides = append(slides, slideEntry{num: n, f: f})
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].num < slides[j].num })

	var b strings.Builder
	if maxBytes > 0 {
		b.Grow(maxBytes)
	}
	for i, s := range slides {
		runs, err := readPPTXSlideText(s.f)
		if err != nil {
			// B15-style policy: warn-and-continue. We don't have slog wired
			// into this package yet; the validator caller will log the
			// outer extraction outcome. Skip silently here — the partner
			// is more likely to read a truncated deck than a hard fail.
			continue
		}
		for _, r := range runs {
			if i > 0 || b.Len() > 0 {
				if !appendCapped(&b, "\n", maxBytes) {
					return b.String(), nil
				}
			}
			if !appendCapped(&b, r, maxBytes) {
				return b.String(), nil
			}
		}
	}
	return b.String(), nil
}

// ExtractDOCXText concatenates every <w:t> run from word/document.xml in a
// .docx file. Same maxBytes / rune-boundary semantics as ExtractPPTXText.
func ExtractDOCXText(path string, maxBytes int) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("validator: open docx %s: %w", path, err)
	}
	defer func() { _ = zr.Close() }()

	var doc *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			doc = f
			break
		}
	}
	if doc == nil {
		return "", fmt.Errorf("validator: docx %s missing word/document.xml", path)
	}

	runs, err := readDOCXBodyText(doc)
	if err != nil {
		return "", fmt.Errorf("validator: parse docx %s: %w", path, err)
	}

	var b strings.Builder
	if maxBytes > 0 {
		b.Grow(maxBytes)
	}
	for i, r := range runs {
		if i > 0 {
			if !appendCapped(&b, "\n", maxBytes) {
				return b.String(), nil
			}
		}
		if !appendCapped(&b, r, maxBytes) {
			return b.String(), nil
		}
	}
	return b.String(), nil
}

// readPPTXSlideText extracts every <a:t> run from a single slide XML file.
func readPPTXSlideText(f *zip.File) ([]string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return readTextElements(rc, "t", "http://schemas.openxmlformats.org/drawingml/2006/main")
}

// readDOCXBodyText extracts every <w:t> run from a docx body.
func readDOCXBodyText(f *zip.File) ([]string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return readTextElements(rc, "t", "http://schemas.openxmlformats.org/wordprocessingml/2006/main")
}

// readTextElements walks the XML stream and returns the text content of
// every <ns:local> element it encounters. Empty/whitespace-only runs are
// skipped to match the Python `if elem.text and elem.text.strip()` filter.
func readTextElements(r io.Reader, local, ns string) ([]string, error) {
	dec := xml.NewDecoder(r)

	var out []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != local || start.Name.Space != ns {
			continue
		}
		var inner string
		if err := dec.DecodeElement(&inner, &start); err != nil {
			return out, err
		}
		trimmed := strings.TrimSpace(inner)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out, nil
}

// slideNumber extracts the slide index from a path like
// `ppt/slides/slide12.xml`. Returns (n, true) when matched, else (0, false).
func slideNumber(path string) (int, bool) {
	const prefix = "ppt/slides/slide"
	const suffix = ".xml"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return 0, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if body == "" {
		return 0, false
	}
	n := 0
	for _, c := range body {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// appendCapped writes s into b but stops at maxBytes. If the write would
// overflow, it truncates on a rune boundary and returns false (caller
// should stop appending). maxBytes <= 0 means "no limit".
func appendCapped(b *strings.Builder, s string, maxBytes int) bool {
	if maxBytes <= 0 {
		b.WriteString(s)
		return true
	}
	remaining := maxBytes - b.Len()
	if remaining <= 0 {
		return false
	}
	if len(s) <= remaining {
		b.WriteString(s)
		return true
	}
	// Truncate on a rune boundary (B4).
	cut := remaining
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	b.WriteString(s[:cut])
	return false
}
