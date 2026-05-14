package report

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ReportFile is one validation_summary_<ts>.md file on disk.
type ReportFile struct {
	Path      string // absolute path
	Timestamp string // raw "YYYYMMDD_HHMMSS" (or whatever followed validation_summary_)
}

// ParsedResult is the per-control projection produced by parseSummary.
// status is one of "PASS"/"FAIL"/"WAIVED" — matching the Python diff
// renderer's bucket names so downstream wording stays parity-clean.
type ParsedResult struct {
	Status    string
	Reasoning string
}

// reportNameRE captures the timestamp portion of a Python-style summary
// filename. The Go validator writes `validation_summary_<ts>.md` to
// match the Python report layout.
var reportNameRE = regexp.MustCompile(`^validation_summary_(.+)\.md$`)

// ListReports enumerates `<partnerFolder>/reports/summary/validation_summary_*.md`
// in chronological order (filename-lexicographic, which matches the Python
// `sorted(...)` behavior because timestamps are zero-padded).
func ListReports(partnerFolder string) ([]ReportFile, error) {
	dir := filepath.Join(partnerFolder, "reports", "summary")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("report: %s does not exist (run validation at least once first)", dir)
		}
		return nil, fmt.Errorf("report: read %s: %w", dir, err)
	}
	var out []ReportFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := reportNameRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		out = append(out, ReportFile{
			Path:      filepath.Join(dir, e.Name()),
			Timestamp: m[1],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out, nil
}

// FindReport returns the report whose filename or timestamp contains the
// given substring (mirroring Python's fuzzy match). Empty match returns
// (nil, nil) so callers can default to first/last.
func FindReport(reports []ReportFile, nameOrTS string) *ReportFile {
	if nameOrTS == "" {
		return nil
	}
	for i := range reports {
		if strings.Contains(filepath.Base(reports[i].Path), nameOrTS) ||
			strings.Contains(reports[i].Timestamp, nameOrTS) {
			return &reports[i]
		}
	}
	return nil
}

// controlHeadingRE matches a `### <icon> <CONTROL>` heading line. The Go
// renderer emits headings without leading icons; the Python renderer
// included them. We accept either form on read.
var controlHeadingRE = regexp.MustCompile(`^###\s+(?:[^\sA-Z]+\s+)?([A-Z][A-Z0-9]*-[A-Z0-9-]+)`)

// ParseSummary reads a validation_summary_<ts>.md file from disk and
// returns a per-control map of status + reasoning. The parser is forgiving
// — anything it can't classify becomes status "FAIL" with the reasoning
// preserved verbatim — to match the Python diff behaviour.
func ParseSummary(path string) (map[string]ParsedResult, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied
	if err != nil {
		return nil, fmt.Errorf("report: read %s: %w", path, err)
	}
	return parseSummaryBytes(data)
}

func parseSummaryBytes(data []byte) (map[string]ParsedResult, error) {
	out := map[string]ParsedResult{}
	currentControl := ""
	currentSection := "" // "passed" | "failed" | ""
	currentReasonLines := []string{}
	inReasonBlock := false

	flush := func() {
		if currentControl == "" {
			return
		}
		status := "FAIL"
		if currentSection == "passed" {
			status = "PASS"
		}
		out[currentControl] = ParsedResult{
			Status:    status,
			Reasoning: strings.TrimSpace(strings.Join(currentReasonLines, "\n")),
		}
	}

	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimSpace(line)

		// Track which section we're in by H2.
		if strings.HasPrefix(line, "## ") {
			heading := strings.ToLower(strings.TrimSpace(line[3:]))
			flush()
			currentControl = ""
			inReasonBlock = false
			currentReasonLines = nil
			switch {
			case strings.Contains(heading, "passed"):
				currentSection = "passed"
			case strings.Contains(heading, "failed"):
				currentSection = "failed"
			default:
				currentSection = ""
			}
			continue
		}

		// New control heading.
		if m := controlHeadingRE.FindStringSubmatch(line); m != nil {
			flush()
			currentControl = m[1]
			currentReasonLines = nil
			inReasonBlock = false
			continue
		}

		if currentControl == "" {
			continue
		}

		// Reason block markers. The Python parser opens on `**Reason**:`,
		// then opens the fenced block on the first ``` (which we *skip*),
		// and closes only on a subsequent ``` once content has been seen.
		if trim == "**Reason**:" {
			inReasonBlock = true
			continue
		}
		if trim == "```" && inReasonBlock {
			if len(currentReasonLines) > 0 {
				inReasonBlock = false
			}
			continue
		}
		if inReasonBlock {
			currentReasonLines = append(currentReasonLines, line)
		}
	}
	flush()
	return out, nil
}

// RenderDiff produces the markdown diff between two parsed reports.
// Sections (in order) match the Python `_handle_diff` output:
//
//   - Changed Controls
//   - Still Failing
//   - New in latest run
//   - Not in latest run
//   - Still Passing
//
// run1Label / run2Label are the timestamp strings shown in the header
// line. The trailing "Available reports" footer is left to callers since
// it's tied to the on-disk listing.
func RenderDiff(partnerName, run1Label, run2Label string,
	results1, results2 map[string]ParsedResult) string {

	var b strings.Builder

	all := union(keys(results1), keys(results2))
	sort.Strings(all)

	type changeRow struct {
		ID                   string
		OldStatus, NewStatus string
		OldReason, NewReason string
	}
	var changed []changeRow
	var unchangedPass, unchangedFail []string
	type addedRow struct {
		ID     string
		Status string
	}
	var newInRun2 []addedRow
	type removedRow struct {
		ID     string
		Status string
	}
	var removedInRun2 []removedRow

	for _, id := range all {
		r1, in1 := results1[id]
		r2, in2 := results2[id]
		switch {
		case !in1:
			newInRun2 = append(newInRun2, addedRow{ID: id, Status: r2.Status})
		case !in2:
			removedInRun2 = append(removedInRun2, removedRow{ID: id, Status: r1.Status})
		case r1.Status != r2.Status:
			changed = append(changed, changeRow{
				ID: id, OldStatus: r1.Status, NewStatus: r2.Status,
				OldReason: r1.Reasoning, NewReason: r2.Reasoning,
			})
		case r1.Status == "PASS":
			unchangedPass = append(unchangedPass, id)
		default:
			unchangedFail = append(unchangedFail, id)
		}
	}

	fmt.Fprintf(&b, "# Validation Diff — %s\n\n", partnerName)
	fmt.Fprintf(&b, "**Comparing:** `%s` (%d controls) → `%s` (%d controls)\n",
		run1Label, len(results1), run2Label, len(results2))
	fmt.Fprintf(&b, "**Changed:** %d | **Unchanged Pass:** %d | **Unchanged Fail:** %d\n\n",
		len(changed), len(unchangedPass), len(unchangedFail))

	if len(changed) > 0 {
		b.WriteString("## Changed Controls\n\n")
		for _, c := range changed {
			fmt.Fprintf(&b, "### %s: %s → %s\n\n", c.ID, c.OldStatus, c.NewStatus)
			if c.OldReason != "" {
				fmt.Fprintf(&b, "**Previous reasoning:** %s\n\n", c.OldReason)
			}
			if c.NewReason != "" {
				fmt.Fprintf(&b, "**Current reasoning:** %s\n\n", c.NewReason)
			}
		}
	}

	if len(unchangedFail) > 0 {
		b.WriteString("## Still Failing\n\n")
		for _, id := range unchangedFail {
			fmt.Fprintf(&b, "### %s\n", id)
			if r := results2[id]; r.Reasoning != "" {
				fmt.Fprintf(&b, "→ %s\n", r.Reasoning)
			}
			b.WriteString("\n")
		}
	}

	if len(newInRun2) > 0 {
		fmt.Fprintf(&b, "## New in latest run (%d controls)\n\n", len(newInRun2))
		for _, n := range newInRun2 {
			fmt.Fprintf(&b, "- %s: %s\n", n.ID, n.Status)
		}
		b.WriteString("\n")
	}

	if len(removedInRun2) > 0 {
		fmt.Fprintf(&b, "## Not in latest run (%d controls)\n\n", len(removedInRun2))
		for _, r := range removedInRun2 {
			fmt.Fprintf(&b, "- %s (was %s)\n", r.ID, r.Status)
		}
		b.WriteString("\n")
	}

	if len(unchangedPass) > 0 {
		fmt.Fprintf(&b, "## Still Passing (%d controls)\n\n", len(unchangedPass))
		b.WriteString(strings.Join(unchangedPass, ", "))
		b.WriteString("\n\n")
	}

	return b.String()
}

// RenderTimeline produces the table view across reports. Each row is
// {#i, date, totals, pass-rate}. Rendered as GitHub-flavored markdown
// tables.
//
// reports must be in chronological order (ListReports returns them that
// way already).
func RenderTimeline(partnerName string, reports []ReportFile) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Validation Timeline — %s\n\n", partnerName)
	b.WriteString("| Run | Date | Controls | Passed | Failed | Pass Rate |\n")
	b.WriteString("|-----|------|----------|--------|--------|-----------|\n")

	for i, r := range reports {
		parsed, err := ParseSummary(r.Path)
		if err != nil {
			return "", err
		}
		passed, failed := 0, 0
		for _, p := range parsed {
			if p.Status == "PASS" {
				passed++
			} else {
				failed++
			}
		}
		total := passed + failed
		passRate := "N/A"
		if total > 0 {
			passRate = fmt.Sprintf("%d%%", passed*100/total)
		}
		fmt.Fprintf(&b, "| #%d | %s | %d | %d | %d | %s |\n",
			i+1, formatTimestamp(r.Timestamp), total, passed, failed, passRate)
	}

	b.WriteString("\nUse `mode: custom` with `run1`/`run2` to compare two specific runs.\n")
	return b.String(), nil
}

// formatTimestamp turns "20260513_103000" into "2026-05-13 10:30". Falls
// back to the raw input if it doesn't look like the expected pattern.
func formatTimestamp(ts string) string {
	if len(ts) < 8 {
		return ts
	}
	out := ts[:4] + "-" + ts[4:6] + "-" + ts[6:8]
	if len(ts) >= 13 && ts[8] == '_' {
		out += " " + ts[9:11] + ":" + ts[11:13]
	}
	return out
}

// keys / union helpers for the diff core.
func keys(m map[string]ParsedResult) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func union(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, v := range a {
		seen[v] = struct{}{}
	}
	for _, v := range b {
		seen[v] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}
