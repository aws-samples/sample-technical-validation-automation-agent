package validator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// writeText writes a tiny text file at path with the given contents so
// processFiles has something to route. Used as a stand-in for partner
// supporting docs across the cap tests.
func writeText(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// docBlockPayload is a tiny accessor that returns the raw bytes inside a
// Bedrock document content block, so tests can inspect appendix
// contents.
func docBlockPayload(t *testing.T, b bedrocktypes.ContentBlock) []byte {
	t.Helper()
	doc, ok := b.(*bedrocktypes.ContentBlockMemberDocument)
	if !ok {
		t.Fatalf("not a document block: %T", b)
	}
	src, ok := doc.Value.Source.(*bedrocktypes.DocumentSourceMemberBytes)
	if !ok {
		t.Fatalf("not a bytes source: %T", doc.Value.Source)
	}
	return src.Value
}

// 7 doc-format files → top 4 binary doc blocks, 3 merged into 1 appendix
// text block. Total content blocks = 5, well under Bedrock's per-call
// MaxDocBlocksPerCall=5 cap.
func TestProcessFiles_DocBlockCap_MergesTailIntoAppendix(t *testing.T) {
	dir := t.TempDir()
	names := []string{"r1.txt", "r2.txt", "r3.txt", "r4.txt", "r5.txt", "r6.txt", "r7.txt"}
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = filepath.Join(dir, n)
		writeText(t, paths[i], "content of "+n)
	}

	v := newBudgetTestValidator(t)
	blocks, err := v.processFiles(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(blocks); got != MaxDocBlocksPerCall {
		t.Fatalf("blocks = %d, want %d", got, MaxDocBlocksPerCall)
	}
	if v.counters.MergedAppendixDocs != 3 {
		t.Errorf("MergedAppendixDocs = %d, want 3", v.counters.MergedAppendixDocs)
	}
	// Last block must be the appendix; verify it concatenates the tail
	// files in the original order.
	appendixBytes := docBlockPayload(t, blocks[len(blocks)-1])
	appendix := string(appendixBytes)
	for _, want := range []string{"r5.txt", "r6.txt", "r7.txt"} {
		if !strings.Contains(appendix, "--- file: "+want+" ---") {
			t.Errorf("appendix missing %s; got:\n%s", want, appendix)
		}
	}
	for _, notWant := range []string{"r1.txt", "r2.txt", "r3.txt", "r4.txt"} {
		if strings.Contains(appendix, "--- file: "+notWant+" ---") {
			t.Errorf("appendix should not contain top-4 file %s", notWant)
		}
	}
}

// At-cap (5 doc-format files) is a no-op: no appendix block.
func TestProcessFiles_DocBlockCap_NoMergeAtFive(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 5)
	for i := range paths {
		paths[i] = filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
		writeText(t, paths[i], "x")
	}
	v := newBudgetTestValidator(t)
	blocks, err := v.processFiles(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 5 {
		t.Fatalf("blocks = %d, want 5", len(blocks))
	}
	if v.counters.MergedAppendixDocs != 0 {
		t.Errorf("expected no appendix merge at cap; counter = %d",
			v.counters.MergedAppendixDocs)
	}
}

// Images don't count toward the doc-block cap. 4 docs + 3 images → 5
// total doc blocks (the 4 binary docs + 1 unused appendix slot is empty
// because doc count is at MaxBinaryDocBlocks, not over) and 3 image
// blocks. Want 4 doc blocks (no appendix) + 3 image blocks = 7 total.
func TestProcessFiles_DocBlockCap_ImagesDontCount(t *testing.T) {
	dir := t.TempDir()
	docPaths := []string{
		filepath.Join(dir, "d1.txt"),
		filepath.Join(dir, "d2.txt"),
		filepath.Join(dir, "d3.txt"),
		filepath.Join(dir, "d4.txt"),
	}
	for _, p := range docPaths {
		writeText(t, p, "doc")
	}

	imgPaths := make([]string, 3)
	for i := range imgPaths {
		imgPaths[i] = filepath.Join(dir, "i"+string(rune('a'+i))+".png")
		// 1x1 PNG is small enough; we just need bytes that processOne
		// recognises as a valid image. Re-use the inline 1x1 PNG.
		writeText(t, imgPaths[i], string(onePixelPNG))
	}

	v := newBudgetTestValidator(t)
	blocks, err := v.processFiles(append(append([]string{}, docPaths...), imgPaths...))
	if err != nil {
		t.Fatal(err)
	}
	docCount, imgCount := 0, 0
	for _, b := range blocks {
		switch b.(type) {
		case *bedrocktypes.ContentBlockMemberDocument:
			docCount++
		case *bedrocktypes.ContentBlockMemberImage:
			imgCount++
		}
	}
	if docCount != 4 {
		t.Errorf("docCount = %d, want 4", docCount)
	}
	if imgCount != 3 {
		t.Errorf("imgCount = %d, want 3", imgCount)
	}
	if v.counters.MergedAppendixDocs != 0 {
		t.Errorf("MergedAppendixDocs = %d, want 0",
			v.counters.MergedAppendixDocs)
	}
}

// onePixelPNG is a minimal-but-valid 1x1 transparent PNG. Image-resize
// paths accept it directly without re-encoding.
var onePixelPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}
