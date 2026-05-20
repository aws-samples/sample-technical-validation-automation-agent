package excel

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"

	"thor-golang/internal/controls"
)

// AppType selects which suffix the extractor appends to suffix-aware control IDs.
// Mirrors the Python --type CLI flag (SOFTWARE | SERVICE).
type AppType string

const (
	// AppTypeSoftware appends -SOFTWARE to suffixed controls.
	AppTypeSoftware AppType = "SOFTWARE"
	// AppTypeService appends -SERVICE to suffixed controls.
	AppTypeService AppType = "SERVICE"
)

// Valid reports whether t is a recognized application type.
func (t AppType) Valid() bool {
	return t == AppTypeSoftware || t == AppTypeService
}

// Response is one extracted partner_response row, in canonical form
// (controlId column always — audit fix B2).
type Response struct {
	ControlID       string
	PartnerResponse string
	EmbeddedLink    string // hyperlink URL from the cell, if any
}

// nanLiterals are the strings the Python pandas pipeline emits when a cell
// is empty (audit B28 ported as-is — sufficient in practice).
var nanLiterals = map[string]struct{}{
	"":     {},
	"nan":  {},
	"NaN":  {},
	"NAN":  {},
	"None": {},
}

// ExtractResponses reads xlsxPath, finds every (controlId, partner_response)
// pair, and writes them to csvPath with a canonical `controlId,partner_response`
// header (audit fix B2).
//
// appType controls SERVICE vs SOFTWARE suffix mapping (audit fix B29: the
// suffix list lives in internal/controls.SuffixControls and is never
// duplicated here). controlList is optional — if non-nil, only matching
// IDs are emitted.
//
// Sheet/column heuristics are ported faithfully from
// extract_partner_responses.py (audit B16/B19/B20/B21 — port as-is):
//   - skip a sheet whose lower-cased name is exactly "introduction"
//   - hardcoded customer-example column indices (F/H/J/L for "GenAI Cust Ex
//     Reqs", E/G/I/K for "Common Cust Example Reqs")
//   - substring matches for "Partner Response" and customer-example sheets.
func ExtractResponses(xlsxPath, csvPath string, appType AppType, controlList []string) error {
	if !appType.Valid() {
		return fmt.Errorf("excel: invalid app type %q (want %q or %q)",
			appType, AppTypeSoftware, AppTypeService)
	}

	rows, err := extract(xlsxPath, appType, controlList)
	if err != nil {
		return err
	}
	return writeCSV(csvPath, rows)
}

// extract parses the workbook into rows in document order. Pure function
// against an opened path — golden tests exercise this directly.
func extract(xlsxPath string, appType AppType, controlList []string) ([]Response, error) {
	f, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		return nil, fmt.Errorf("excel: open %s: %w", xlsxPath, err)
	}
	defer func() { _ = f.Close() }()

	// Build the SuffixControls allowlist into a map for O(1) lookup. B29
	// shared constant — never duplicate.
	suffixSet := make(map[string]struct{}, len(controls.SuffixControls))
	for _, s := range controls.SuffixControls {
		suffixSet[s] = struct{}{}
	}

	// Build the control allowlist + base→suffixed mapping (Python parity).
	// control_mapping records the canonical suffixed form a base control
	// should resolve to when controlList contains a suffixed entry.
	var allowSet map[string]struct{}
	controlMapping := make(map[string]string)
	if controlList != nil {
		allowSet = make(map[string]struct{}, len(controlList))
		for _, c := range controlList {
			allowSet[c] = struct{}{}
			if base, ok := stripKnownSuffix(c); ok {
				controlMapping[base] = c
			}
		}
	}

	var out []Response

	for _, sheet := range f.GetSheetList() {
		// B19 ported as-is: only the literal lowercase "introduction" is skipped.
		if strings.EqualFold(sheet, "introduction") {
			continue
		}

		sheetRows, err := readSheet(f, sheet)
		if err != nil {
			return nil, fmt.Errorf("excel: read sheet %q: %w", sheet, err)
		}

		headerRow, idCol, ok := findHeader(sheetRows)
		if !ok {
			continue
		}

		respCols := responseColumns(sheet, sheetRows, headerRow)
		if len(respCols) == 0 {
			// Match the Python warning surface; not an error.
			fmt.Fprintf(os.Stderr, "WARN excel: sheet %q has no Partner Response columns; skipping\n", sheet)
			continue
		}

		startRow := headerRow + 1
		if isCustomerExampleSheet(sheet) {
			// Python: customer-example sheets have a description row right
			// after the header before data begins.
			startRow = headerRow + 2
		}

		for r := startRow; r < len(sheetRows); r++ {
			row := sheetRows[r]
			if idCol >= len(row) {
				continue
			}
			controlID := strings.TrimSpace(row[idCol])
			if controlID == "" || controlID == "ID" || controlID == "Partner Response" {
				continue
			}
			if _, isNaN := nanLiterals[controlID]; isNaN {
				continue
			}

			outID := controlID
			if _, isSuffix := suffixSet[controlID]; isSuffix {
				outID = fmt.Sprintf("%s-%s", controlID, appType)
			}

			if controlList != nil {
				if _, ok := allowSet[outID]; !ok {
					mapped, mappedOK := controlMapping[controlID]
					if !mappedOK {
						continue
					}
					outID = mapped
				}
			}

			for _, c := range respCols {
				if c >= len(row) {
					continue
				}
				resp := strings.TrimSpace(row[c])
				if resp == "" {
					continue
				}
				if _, isNaN := nanLiterals[resp]; isNaN {
					continue
				}
				// Extract hyperlink from cell if present
				var link string
				cellName, cerr := excelize.CoordinatesToCellName(c+1, r+1)
				if cerr == nil {
					if hasLink, target, lerr := f.GetCellHyperLink(sheet, cellName); lerr == nil && hasLink {
						link = target
					}
				}
				out = append(out, Response{
					ControlID:       outID,
					PartnerResponse: resp,
					EmbeddedLink:    link,
				})
			}
		}
	}

	return out, nil
}

// stripKnownSuffix returns the base ID and true when id ends in -SERVICE or
// -SOFTWARE. Mirrors the Python control_mapping construction in
// extract_partner_responses.extract_responses.
func stripKnownSuffix(id string) (string, bool) {
	for _, suffix := range []string{"-SERVICE", "-SOFTWARE"} {
		if strings.HasSuffix(id, suffix) {
			return id[:len(id)-len(suffix)], true
		}
	}
	return id, false
}

// findHeader locates the row containing the literal cell "ID" and returns
// (rowIndex, colIndex, true). Returns (_, _, false) if the sheet has no
// such header — used to skip non-data sheets.
func findHeader(rows [][]string) (int, int, bool) {
	for r, row := range rows {
		for c, cell := range row {
			if strings.TrimSpace(cell) == "ID" {
				return r, c, true
			}
		}
	}
	return 0, 0, false
}

// responseColumns chooses the columns to read partner responses from for
// the given sheet, mirroring the Python heuristics 1:1.
//
//   - "GenAI Cust Ex Reqs" / "Generative AI Customer Example" → F, H, J, L (5/7/9/11)
//   - "Common Cust Example Reqs" / "Common Customer Example"  → E, G, I, K (4/6/8/10)
//   - else: scan the row after the header (then the header itself) for any
//     cell containing "Partner Response" and return that column.
func responseColumns(sheet string, rows [][]string, headerRow int) []int {
	// For customer example sheets, dynamically scan the sub-header row
	// for "Partner Response" columns. This handles varying Excel template
	// versions where columns may shift.
	if isCustomerExampleSheet(sheet) {
		subHeaderRow := headerRow + 1
		if subHeaderRow < len(rows) {
			var found []int
			for c, cell := range rows[subHeaderRow] {
				if strings.Contains(cell, "Partner Response") {
					found = append(found, c)
				}
			}
			if len(found) > 0 {
				return found
			}
		}
		// Fallback to hardcoded positions if sub-header scan fails
		switch {
		case strings.Contains(sheet, "GenAI Cust Ex Reqs"),
			strings.Contains(sheet, "Generative AI Customer Example"):
			return []int{5, 7, 9, 11} // F, H, J, L
		case strings.Contains(sheet, "Common Cust Example Reqs"),
			strings.Contains(sheet, "Common Customer Example"):
			return []int{4, 6, 8, 10} // E, G, I, K
		}
	}

	// Single-response path. Try the row below the header first, then the
	// header row itself.
	for _, r := range []int{headerRow + 1, headerRow} {
		if r < 0 || r >= len(rows) {
			continue
		}
		var found []int
		for c, cell := range rows[r] {
			if strings.Contains(cell, "Partner Response") {
				found = append(found, c)
			}
		}
		if len(found) > 0 {
			return found
		}
	}
	return nil
}

func isCustomerExampleSheet(sheet string) bool {
	return strings.Contains(sheet, "Cust Ex Reqs") || strings.Contains(sheet, "Customer Example")
}

// readSheet returns the sheet as [][]string using the streaming Rows iterator.
// Required for files >20 MB — partner submissions are unbounded.
func readSheet(f *excelize.File, sheet string) ([][]string, error) {
	it, err := f.Rows(sheet)
	if err != nil {
		return nil, err
	}
	defer func() { _ = it.Close() }()

	var rows [][]string
	for it.Next() {
		cols, err := it.Columns()
		if err != nil {
			return nil, err
		}
		rows = append(rows, cols)
	}
	if err := it.Error(); err != nil {
		return nil, err
	}
	return rows, nil
}

// writeCSV emits the canonical controlId,partner_response form (audit B2).
func writeCSV(path string, rows []Response) error {
	f, err := os.Create(path) //nolint:gosec // path is CLI-supplied output location
	if err != nil {
		return fmt.Errorf("excel: create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	w := csv.NewWriter(f)
	if err := w.Write([]string{controls.ColumnControlID, "partner_response", "embedded_link"}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{r.ControlID, r.PartnerResponse, r.EmbeddedLink}); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	return nil
}

// LoadResponses reads a previously-extracted partner_responses.csv. It
// accepts either `controlId` or `control_id` as the ID column header
// (audit fix B2) and canonicalizes to controlId on the way back.
func LoadResponses(path string) ([]Response, error) {
	f, err := os.Open(path) //nolint:gosec // path is CLI-supplied input
	if err != nil {
		return nil, fmt.Errorf("excel: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("excel: read header from %s: %w", path, err)
	}

	idCol, respCol, linkCol := -1, -1, -1
	for i, h := range header {
		switch controls.CanonicalHeader(h) {
		case controls.ColumnControlID:
			idCol = i
		case "partner_response":
			respCol = i
		case "embedded_link":
			linkCol = i
		}
	}
	if idCol == -1 {
		return nil, fmt.Errorf("%w: got headers %v", controls.ErrControlIDColumnMissing, header)
	}
	if respCol == -1 {
		return nil, fmt.Errorf("excel: missing partner_response column in %s (got headers %v)",
			path, header)
	}

	var out []Response
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("excel: read row from %s: %w", path, err)
		}
		if idCol >= len(rec) || respCol >= len(rec) {
			continue
		}
		link := ""
		if linkCol >= 0 && linkCol < len(rec) {
			link = strings.TrimSpace(rec[linkCol])
		}
		out = append(out, Response{
			ControlID:       strings.TrimSpace(rec[idCol]),
			PartnerResponse: rec[respCol],
			EmbeddedLink:    link,
		})
	}
	return out, nil
}
