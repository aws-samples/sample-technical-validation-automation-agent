package pdf

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	pdfcpuapi "github.com/pdfcpu/pdfcpu/pkg/api"
)

// DefaultMaxBytes is the per-attachment Bedrock document-block size limit.
// Mirrors Python's max_doc_bytes default in thor_validator.process_files.
const DefaultMaxBytes = 4_500_000

// DefaultPagesPerChunk is Python's pdfcpu split target (40-page chunks).
const DefaultPagesPerChunk = 40

// DefaultMaxPages is the per-document page-count threshold above which
// SplitIfNeeded triggers a split. PLAN.md fixes this at 40 (B5 makes the
// detection precise via real page count).
const DefaultMaxPages = 40

// CountPages returns the page count of the PDF at path using pdfcpu.
//
// Audit fix B5: pdfcpu reads the real cross-reference table; the Python
// guess "size / 50 KB" was inaccurate for image-heavy decks.
func CountPages(path string) (int, error) {
	n, err := pdfcpuapi.PageCountFile(path)
	if err != nil {
		return 0, fmt.Errorf("pdf: count pages in %s: %w", path, err)
	}
	return n, nil
}

// Split chunks path into pagesPerChunk-page PDFs in outDir using pdfcpu.
// outDir must exist. Returns the chunk paths in page order. If
// pagesPerChunk <= 0, DefaultPagesPerChunk is used. Pure-Go in-process
// equivalent of `qpdf --pages . start-end --` (audit decision #2 / B8).
func Split(path, outDir string, pagesPerChunk int) ([]string, error) {
	if pagesPerChunk <= 0 {
		pagesPerChunk = DefaultPagesPerChunk
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return nil, fmt.Errorf("pdf: mkdir %s: %w", outDir, err)
	}
	if err := pdfcpuapi.SplitFile(path, outDir, pagesPerChunk, nil); err != nil {
		return nil, fmt.Errorf("pdf: split %s: %w", path, err)
	}

	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	matches, err := filepath.Glob(filepath.Join(outDir, stem+"_*.pdf"))
	if err != nil {
		return nil, fmt.Errorf("pdf: enumerate split chunks: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("pdf: split produced no chunks for %s", path)
	}
	return matches, nil
}

// SplitIfNeeded returns one or more paths suitable for sending to Bedrock as
// document blocks. If the source file is within both byte- and page-limits,
// returns just [path] without copying.
//
// On overshoot, splits with pdfcpu (chunkPages, defaulting to
// DefaultPagesPerChunk). If a chunk still exceeds maxBytes after the split
// (e.g. a single page contains a huge embedded image), the chunk path is
// replaced with a text-extraction fallback file written next to the chunk
// — caller can detect text fallback by extension (.txt).
//
// outDir is where chunks (and any .txt fallbacks) are written. The caller
// owns cleanup of outDir.
func SplitIfNeeded(path, outDir string, maxBytes, maxPages, chunkPages int) ([]string, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}
	if chunkPages <= 0 {
		chunkPages = DefaultPagesPerChunk
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("pdf: stat %s: %w", path, err)
	}

	pages, err := CountPages(path)
	if err != nil {
		return nil, err
	}

	if int(info.Size()) <= maxBytes && pages <= maxPages {
		return []string{path}, nil
	}

	chunks, err := Split(path, outDir, chunkPages)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		ci, err := os.Stat(chunk)
		if err != nil {
			return nil, fmt.Errorf("pdf: stat chunk %s: %w", chunk, err)
		}
		if int(ci.Size()) <= maxBytes {
			out = append(out, chunk)
			continue
		}

		// Chunk still too big — text-extract instead.
		text, err := ExtractText(chunk, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("pdf: extract text fallback for oversized chunk %s: %w", chunk, err)
		}
		txtPath := strings.TrimSuffix(chunk, filepath.Ext(chunk)) + ".txt"
		if err := os.WriteFile(txtPath, []byte(text), 0o600); err != nil {
			return nil, fmt.Errorf("pdf: write text fallback %s: %w", txtPath, err)
		}
		out = append(out, txtPath)
	}
	return out, nil
}

// ExtractText reads path with ledongthuc/pdf and concatenates per-page text.
//
// Audit fix B4: the byte budget is enforced against the encoded UTF-8 size
// (not character count) and truncation lands on a rune boundary so we never
// emit invalid UTF-8.
//
// Audit fix B15: a per-page extraction failure is logged to stderr but does
// not abort the document — extraction continues with the next page.
func ExtractText(path string, maxBytes int) (string, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("pdf: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var b strings.Builder
	b.Grow(maxBytes)
	total := 0

	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN pdf: page %d of %s: %v (skipped)\n", i, path, err)
			continue
		}
		if total >= maxBytes {
			break
		}
		remaining := maxBytes - total
		// Page separator: newline between pages, but only after the first
		// non-empty page so we don't emit a leading newline.
		if total > 0 && remaining > 0 {
			b.WriteByte('\n')
			total++
			remaining--
		}
		if len(text) <= remaining {
			b.WriteString(text)
			total += len(text)
			continue
		}
		// Truncate on a rune boundary.
		cut := remaining
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		b.WriteString(text[:cut])
		break
	}
	return b.String(), nil
}

// alreadySplitRE matches `<stem>_PartN.pdf` where N has at least one digit.
// Mirrors Python check `'Part' in pdf_file.stem and any(c.isdigit() for c in
// pdf_file.stem.split('Part')[-1])` from _auto_split_large_pdfs.
var alreadySplitRE = regexp.MustCompile(`Part[0-9]+`)

// IsAlreadySplit reports whether path looks like it was produced by a prior
// split run. Used by callers to preserve idempotence (audit 1C.5).
func IsAlreadySplit(path string) bool {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return alreadySplitRE.MatchString(stem)
}

// ErrEncrypted indicates the PDF refused decryption. Caller should surface
// an actionable message rather than a stack trace.
var ErrEncrypted = errors.New("pdf: file is encrypted (no password supplied)")
