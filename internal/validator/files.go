package validator

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"thor-golang/internal/pdf"
)

// MaxDocBytes is the per-document Bedrock attachment size cap. Mirrors
// Python `max_doc_bytes=4_500_000` in process_files.
const MaxDocBytes = 4_500_000

// MaxPDFPagesPerCall is the Bedrock Converse cap on aggregate PDF pages
// across all document blocks in a single request. Bedrock returns
// `ValidationException: A maximum of 100 PDF pages may be provided` once
// this is exceeded, so we keep a small headroom (split chunks below this
// fall through unchanged).
const MaxPDFPagesPerCall = 95

// MaxDocBlocksPerCall is the Bedrock Converse cap on the number of
// `document` content blocks in a single request. Anthropic Claude on
// Bedrock rejects requests with >5 document blocks
// (`ValidationException: input must have 5 or fewer documents`).
//
// We treat "5" as a hard ceiling and keep one slot reserved for an
// "appendix" text block, so the practical binary-document cap is 4. The
// remaining files (in mapper rank order — least relevant first to be
// demoted) are text-extracted and concatenated into the appendix.
//
// Images are NOT documents and pass through unaffected.
const MaxDocBlocksPerCall = 5

// MaxBinaryDocBlocks is the practical cap on full-fidelity (binary)
// document blocks per call: MaxDocBlocksPerCall - 1 to leave room for
// the merged-text appendix block.
const MaxBinaryDocBlocks = MaxDocBlocksPerCall - 1

// extension -> bedrock document format. Mirrors the routing table in
// thor_validator._extract_text_from_file and process_files.
var documentFormatByExt = map[string]bedrocktypes.DocumentFormat{
	".pdf":  bedrocktypes.DocumentFormatPdf,
	".xlsx": bedrocktypes.DocumentFormatXlsx,
	".xls":  bedrocktypes.DocumentFormatXls,
	".docx": bedrocktypes.DocumentFormatDocx,
	".doc":  bedrocktypes.DocumentFormatDoc,
	".csv":  bedrocktypes.DocumentFormatCsv,
	".html": bedrocktypes.DocumentFormatHtml,
	".txt":  bedrocktypes.DocumentFormatTxt,
	".md":   bedrocktypes.DocumentFormatMd,
}

var imageFormatByExt = map[string]bedrocktypes.ImageFormat{
	".png":  bedrocktypes.ImageFormatPng,
	".jpg":  bedrocktypes.ImageFormatJpeg,
	".jpeg": bedrocktypes.ImageFormatJpeg,
}

// Counters surfaces a few per-validation metrics that runlog and
// dogfooding (PLAN.md Phase 3.5) want to track.
type Counters struct {
	ProcessedPPTX int
	ProcessedDOCX int
	ResizedImages int
	OversizeText  int
	// DemotedPDFs counts PDFs sent as extracted text (instead of binary
	// document blocks) because the supporting_docs aggregate exceeded
	// MaxPDFPagesPerCall.
	DemotedPDFs int
	// MergedAppendixDocs counts documents that were merged into a single
	// appendix text block because the per-call doc-block limit would
	// have been exceeded (MaxDocBlocksPerCall).
	MergedAppendixDocs int
}

// processFiles routes a slice of evidence file paths into Bedrock content
// blocks. The routing rules are:
//
//   - .pdf, .xlsx, .xls, .docx, .doc, .csv, .html, .txt, .md   → document block (raw bytes)
//   - .png, .jpg, .jpeg                                        → image block (after 8000px-cap resize)
//   - .pptx                                                    → ALWAYS extract text → document block (txt)
//   - any document > MaxDocBytes                               → text-extraction fallback → document block (txt)
//
// Audit decisions / fixes:
//   - PLAN.md decision #17: small .pptx files are read in full (Python
//     skips them on the size-fallback path).
//   - B4 / B15: text extractors handle byte budgets and per-page failures
//     internally.
//   - B13: the image cache is keyed on the absolute path string. Symlinks
//     and relative paths can cause re-resize; accepted tradeoff.
//
// Bedrock per-call caps enforced here:
//   - MaxPDFPagesPerCall (≤95 aggregate PDF pages): handled by
//     planPDFDemotions before per-file routing.
//   - MaxDocBlocksPerCall (≤5 document blocks): paths are received in
//     mapper rank order (most relevant first); the top MaxBinaryDocBlocks
//     get full-fidelity binary doc blocks, the remaining doc-format
//     files are text-extracted and concatenated into a single appendix
//     text block. Image blocks don't count toward this cap.
//
// Unknown extensions are skipped with a warning.
func (v *Validator) processFiles(paths []string) ([]bedrocktypes.ContentBlock, error) {
	demote := v.planPDFDemotions(paths, MaxPDFPagesPerCall)
	mergeIntoAppendix := v.planAppendixMerge(paths)

	out := make([]bedrocktypes.ContentBlock, 0, len(paths)+1)
	var appendixParts []string
	for _, p := range paths {
		if mergeIntoAppendix[p] {
			text, err := v.extractTextForAppendix(p)
			if err != nil {
				v.logger.Warn("appendix text extraction failed; skipping file",
					slog.String("path", p), slog.String("err", err.Error()))
				continue
			}
			if text == "" {
				continue
			}
			appendixParts = append(appendixParts,
				fmt.Sprintf("--- file: %s ---\n%s", filepath.Base(p), text))
			v.counters.MergedAppendixDocs++
			continue
		}
		blocks, err := v.processOne(p, demote[p])
		if err != nil {
			v.logger.Warn("file processing failed; skipping",
				slog.String("path", p), slog.String("err", err.Error()))
			continue
		}
		out = append(out, blocks...)
	}

	if len(appendixParts) > 0 {
		merged := strings.Join(appendixParts, "\n\n")
		v.logger.Info("appendix block merged; tail files demoted to single text block",
			slog.Int("merged_count", len(appendixParts)),
			slog.Int("appendix_bytes", len(merged)))
		out = append(out, textDocBlock("appendix", merged))
	}
	return out, nil
}

// planAppendixMerge selects which paths must be merged into the appendix
// text block to keep the per-call document-block count under
// MaxDocBlocksPerCall. When the total doc-format file count exceeds the
// cap, the first MaxBinaryDocBlocks doc-format paths (in input order —
// already mapper-rank-ordered when an evidence map is in play) keep
// their full-fidelity routing; the rest are demoted into a single
// appendix block.
//
// At or below the cap (≤MaxDocBlocksPerCall doc-format files) every
// file is sent as a binary doc block — no appendix is created.
//
// Image paths don't count toward the cap, so they're skipped here and
// pass through processOne unchanged.
func (v *Validator) planAppendixMerge(paths []string) map[string]bool {
	docPaths := make([]string, 0, len(paths))
	for _, p := range paths {
		ext := strings.ToLower(filepath.Ext(p))
		if _, isImg := imageFormatByExt[ext]; isImg {
			continue
		}
		if _, isDoc := documentFormatByExt[ext]; !isDoc {
			// Unknown extension — processOne will skip it with a
			// warning; don't count against the cap.
			continue
		}
		docPaths = append(docPaths, p)
	}
	out := make(map[string]bool)
	if len(docPaths) <= MaxDocBlocksPerCall {
		return out
	}
	for _, p := range docPaths[MaxBinaryDocBlocks:] {
		out[p] = true
	}
	v.logger.Info("doc-block cap exceeded; merging tail into appendix",
		slog.Int("cap", MaxDocBlocksPerCall),
		slog.Int("binary_kept", MaxBinaryDocBlocks),
		slog.Int("merged_count", len(out)))
	return out
}

// extractTextForAppendix runs the format-appropriate text extractor for
// a path that's been demoted into the appendix block. Empty extraction
// is treated as "skip silently" — better than crashing the whole control
// over a single file we couldn't read.
func (v *Validator) extractTextForAppendix(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return pdf.ExtractText(path, MaxDocBytes)
	case ".pptx":
		return ExtractPPTXText(path, MaxDocBytes)
	case ".docx":
		return ExtractDOCXText(path, MaxDocBytes)
	case ".txt", ".md", ".csv", ".html":
		return readCappedText(path, MaxDocBytes)
	case ".xlsx", ".xls":
		// Excel text extraction is brittle; surface as a warning so the
		// operator can move the file out of supporting_docs if it isn't
		// genuinely evidence. Returning empty drops it from the appendix.
		v.logger.Warn("xlsx/xls cannot be merged into appendix; file dropped",
			slog.String("path", path))
		return "", nil
	default:
		return "", nil
	}
}

// planPDFDemotions decides which PDFs must be sent as extracted text
// rather than binary document blocks so the aggregate PDF page count for
// a single Converse call stays within Bedrock's per-call limit
// (MaxPDFPagesPerCall, currently 95 with headroom under the 100-page
// hard limit).
//
// Strategy: count pages on every PDF, and while the running total exceeds
// the budget, demote the PDF with the largest page count. Largest-first
// is chosen because it removes the most pages per demotion (so we keep
// binary fidelity for as many small PDFs as possible) and because the
// long-tail oversized PDFs are typically presentation decks where text
// extraction loses the least relative to short, layout-heavy diagrams.
//
// PDFs that fail page counting are conservatively demoted — better to
// extract text than crash the whole control with a 400 from Bedrock.
//
// Returns a set of paths that should be text-extracted. PDFs not in the
// set are sent as binary document blocks (their existing path).
func (v *Validator) planPDFDemotions(paths []string, budget int) map[string]bool {
	if budget <= 0 {
		return nil
	}

	type pdfEntry struct {
		path  string
		pages int
	}
	pdfs := make([]pdfEntry, 0, len(paths))
	totalPages := 0
	for _, p := range paths {
		if strings.ToLower(filepath.Ext(p)) != ".pdf" {
			continue
		}
		n, err := pdf.CountPages(p)
		if err != nil {
			v.logger.Warn("pdf page-count failed; demoting to text",
				slog.String("path", p), slog.String("err", err.Error()))
			n = budget + 1 // forces demotion
		}
		pdfs = append(pdfs, pdfEntry{path: p, pages: n})
		totalPages += n
	}

	if totalPages <= budget {
		return nil
	}

	// Sort descending by page count, stable on path for determinism.
	sort.SliceStable(pdfs, func(i, j int) bool {
		if pdfs[i].pages != pdfs[j].pages {
			return pdfs[i].pages > pdfs[j].pages
		}
		return pdfs[i].path < pdfs[j].path
	})

	demote := make(map[string]bool)
	for _, e := range pdfs {
		if totalPages <= budget {
			break
		}
		demote[e.path] = true
		totalPages -= e.pages
	}
	v.logger.Info("pdf page-budget exceeded; demoting largest PDFs to text",
		slog.Int("budget", budget),
		slog.Int("demoted_count", len(demote)),
		slog.Int("remaining_pages", totalPages))
	return demote
}

func (v *Validator) processOne(path string, demoteToText bool) ([]bedrocktypes.ContentBlock, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("not a file: %s", path)
	}
	if info.Size() == 0 {
		v.logger.Info("skipping empty file", slog.String("path", path))
		return nil, nil
	}

	ext := strings.ToLower(filepath.Ext(path))

	// PDF demotion path: page-budget pre-pass selected this PDF for text
	// extraction so the aggregate Converse request stays under Bedrock's
	// 100-page cap.
	if ext == ".pdf" && demoteToText {
		txt, err := pdf.ExtractText(path, MaxDocBytes)
		if err != nil {
			return nil, fmt.Errorf("pdf text demotion: %w", err)
		}
		if txt == "" {
			return nil, errors.New("pdf text demotion produced empty output")
		}
		v.counters.DemotedPDFs++
		v.logger.Info("file routed",
			slog.String("path", path), slog.String("ext", ext),
			slog.String("route", "pdf-demoted-text"), slog.Int64("bytes", info.Size()))
		return []bedrocktypes.ContentBlock{textDocBlock(path, txt)}, nil
	}

	// .pptx — always text-extract, regardless of size (PLAN decision #17).
	if ext == ".pptx" {
		txt, err := ExtractPPTXText(path, MaxDocBytes)
		if err != nil {
			return nil, fmt.Errorf("pptx extract: %w", err)
		}
		v.counters.ProcessedPPTX++
		v.logger.Info("file routed",
			slog.String("path", path), slog.String("ext", ext),
			slog.String("route", "pptx-text"), slog.Int64("bytes", info.Size()))
		return []bedrocktypes.ContentBlock{textDocBlock(path, txt)}, nil
	}

	// Images — resize then send as image block.
	if imgFormat, ok := imageFormatByExt[ext]; ok {
		raw, err := os.ReadFile(path) //nolint:gosec // path is from CLI/MCP arg
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		canonical := canonicalImageExt(ext)
		resized, err := resizeIfNeeded(raw, canonical, absPath(path), v.imageCache)
		if err != nil {
			return nil, fmt.Errorf("image resize: %w", err)
		}
		if len(resized) != len(raw) {
			v.counters.ResizedImages++
		}
		v.logger.Info("file routed",
			slog.String("path", path), slog.String("ext", ext),
			slog.String("route", "image"), slog.Int64("bytes", info.Size()))
		return []bedrocktypes.ContentBlock{
			&bedrocktypes.ContentBlockMemberImage{Value: bedrocktypes.ImageBlock{
				Format: imgFormat,
				Source: &bedrocktypes.ImageSourceMemberBytes{Value: resized},
			}},
		}, nil
	}

	// Documents — supported formats go in as raw bytes; oversized ones
	// fall back to text extraction. Unknown extensions are skipped.
	docFormat, ok := documentFormatByExt[ext]
	if !ok {
		v.logger.Info("skipping file with unsupported extension",
			slog.String("path", path), slog.String("ext", ext))
		return nil, nil
	}

	if info.Size() <= MaxDocBytes {
		raw, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		v.logger.Info("file routed",
			slog.String("path", path), slog.String("ext", ext),
			slog.String("route", "document"), slog.Int64("bytes", info.Size()))
		return []bedrocktypes.ContentBlock{docBlockRaw(path, docFormat, raw)}, nil
	}

	// Oversize fallback: extract text for known doc types, send as txt.
	v.logger.Info("file too large; falling back to text extraction",
		slog.String("path", path), slog.String("ext", ext),
		slog.Int64("bytes", info.Size()))
	v.counters.OversizeText++

	txt, err := v.extractTextFallback(path, ext)
	if err != nil {
		return nil, fmt.Errorf("text fallback: %w", err)
	}
	if txt == "" {
		return nil, errors.New("text fallback produced empty output")
	}
	if ext == ".docx" {
		v.counters.ProcessedDOCX++
	}
	return []bedrocktypes.ContentBlock{textDocBlock(path, txt)}, nil
}

func (v *Validator) extractTextFallback(path, ext string) (string, error) {
	switch ext {
	case ".pdf":
		return pdf.ExtractText(path, MaxDocBytes)
	case ".docx":
		return ExtractDOCXText(path, MaxDocBytes)
	case ".xlsx", ".xls":
		// Excel oversize fallback is rare and best handled by the
		// validator skipping the file rather than mis-extracting; the
		// Python code uses pandas .to_string() which often produces
		// noise. Return an actionable error so the operator knows.
		return "", fmt.Errorf("oversize Excel files are not supported in fallback extraction")
	case ".html", ".txt", ".md", ".csv":
		// Plain-text formats: read up to MaxDocBytes directly.
		return readCappedText(path, MaxDocBytes)
	default:
		return "", fmt.Errorf("no fallback extractor for %s", ext)
	}
}

// readCappedText reads up to maxBytes of a file as UTF-8, truncating on a
// rune boundary if needed.
func readCappedText(path string, maxBytes int) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if maxBytes > 0 {
		b.Grow(maxBytes)
	}
	if !appendCapped(&b, string(data), maxBytes) {
		return b.String(), nil
	}
	return b.String(), nil
}

// docBlockRaw assembles a Bedrock document content block from raw bytes.
func docBlockRaw(path string, fmtEnum bedrocktypes.DocumentFormat, raw []byte) bedrocktypes.ContentBlock {
	return &bedrocktypes.ContentBlockMemberDocument{
		Value: bedrocktypes.DocumentBlock{
			Name:   aws.String(safeDocName(path)),
			Format: fmtEnum,
			Source: &bedrocktypes.DocumentSourceMemberBytes{Value: raw},
		},
	}
}

// textDocBlock wraps an extracted text payload as a txt-format document
// block (matches Python `processed_files.append((text_bytes, 'txt', ...))`).
func textDocBlock(path, text string) bedrocktypes.ContentBlock {
	return &bedrocktypes.ContentBlockMemberDocument{
		Value: bedrocktypes.DocumentBlock{
			Name:   aws.String(safeDocName(path)),
			Format: bedrocktypes.DocumentFormatTxt,
			Source: &bedrocktypes.DocumentSourceMemberBytes{Value: []byte(text)},
		},
	}
}

// safeDocName generates a Bedrock-acceptable document name. The spec
// allows alphanumerics, whitespace (≤1 in a row), hyphens, parentheses,
// square brackets — but not periods or underscores. Python uses a
// hex-encoded UUID; we do the same with crypto/rand.
func safeDocName(_ string) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func canonicalImageExt(ext string) string {
	if ext == ".jpg" {
		return "jpeg"
	}
	return strings.TrimPrefix(ext, ".")
}

// absPath turns p into an absolute path for cache keying. On error,
// returns p unchanged — the cache key just becomes less stable, which is
// acceptable per B13.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// ListEvidenceFiles returns the supporting_docs entries for partner
// validation. Exported wrapper around listEvidenceFiles so the evidence
// mapper (internal/evidence) can enumerate the same file set the
// validator does.
func ListEvidenceFiles(supportingDocsDir string) ([]string, error) {
	return listEvidenceFiles(supportingDocsDir)
}

// ProcessFileForEvidence builds Bedrock content blocks for a single
// supporting_docs file using the same routing rules ValidateControl
// applies — PPTX text extraction, image resize, oversize fallback —
// minus the cross-file PDF page budget (which is meaningless when only
// one file is in flight). Used by the evidence mapper to send each
// document to Bedrock individually for relevance triage.
func (v *Validator) ProcessFileForEvidence(path string) ([]bedrocktypes.ContentBlock, error) {
	return v.processOne(path, false)
}

// listEvidenceFiles returns the supporting_docs entries for partner
// validation. Hidden files (`.foo`), Office tilde locks (`~$foo`), and
// directories are skipped — matching the Python glob+filter chain.
func listEvidenceFiles(supportingDocsDir string) ([]string, error) {
	entries, err := os.ReadDir(supportingDocsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", supportingDocsDir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "~$") || strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, filepath.Join(supportingDocsDir, name))
	}
	return out, nil
}
