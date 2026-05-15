package controls

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"thor-golang/internal/prompts"
)

// Control is one row from CONTEXT.csv: a control identifier and the
// per-control prompt context the validator sends to Bedrock.
type Control struct {
	ID            string
	PromptContext string
}

// Header constants. Audit fix B2: callers always emit/read controlId
// internally, but we accept either form on read to handle older partner
// templates. CSV writers in this codebase MUST use ColumnControlID.
const (
	ColumnControlID      = "controlId"
	columnControlIDAlias = "control_id"
	columnPromptContext  = "prompt_context"
	columnPartnerResp    = "partner_response"
)

// ErrControlIDColumnMissing is returned when a CSV header has neither
// "controlId" nor "control_id".
var ErrControlIDColumnMissing = errors.New("controls: missing controlId column")

// Load parses the embedded CONTEXT.csv into a slice of Control rows in
// file order.
func Load() ([]Control, error) {
	raw, err := prompts.ContextCSV()
	if err != nil {
		return nil, err
	}
	return parseContextCSV(bytes.NewReader(raw))
}

// LoadMap is a convenience wrapper that returns the parsed controls keyed
// by ID. Errors if the source contains duplicate IDs.
func LoadMap() (map[string]Control, error) {
	rows, err := Load()
	if err != nil {
		return nil, err
	}
	out := make(map[string]Control, len(rows))
	for _, c := range rows {
		if _, dup := out[c.ID]; dup {
			return nil, fmt.Errorf("controls: duplicate control ID in CONTEXT.csv: %q", c.ID)
		}
		out[c.ID] = c
	}
	return out, nil
}

func parseContextCSV(r io.Reader) ([]Control, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("controls: read CONTEXT.csv header: %w", err)
	}

	idCol, contextCol := -1, -1
	for i, name := range header {
		switch strings.TrimSpace(name) {
		case ColumnControlID, columnControlIDAlias:
			idCol = i
		case columnPromptContext:
			contextCol = i
		}
	}
	if idCol == -1 {
		return nil, fmt.Errorf("%w: got headers %v", ErrControlIDColumnMissing, header)
	}
	if contextCol == -1 {
		return nil, fmt.Errorf("controls: missing %s column in CONTEXT.csv (got headers %v)",
			columnPromptContext, header)
	}

	var out []Control
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("controls: read CONTEXT.csv row: %w", err)
		}
		if idCol >= len(record) || contextCol >= len(record) {
			continue
		}
		id := strings.TrimSpace(record[idCol])
		if id == "" {
			continue
		}
		out = append(out, Control{
			ID:            id,
			PromptContext: record[contextCol],
		})
	}
	return out, nil
}

// CanonicalHeader normalizes a CSV header value so that callers can accept
// either `controlId` or `control_id` and feed the result back as the
// canonical column name. Used by internal/excel when reading partner CSVs.
func CanonicalHeader(name string) string {
	switch strings.TrimSpace(name) {
	case ColumnControlID, columnControlIDAlias:
		return ColumnControlID
	case columnPartnerResp:
		return columnPartnerResp
	default:
		return strings.TrimSpace(name)
	}
}
