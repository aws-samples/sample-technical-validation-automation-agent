package controls

import "fmt"

// SuffixDriftWarnings returns one warning string per CONTEXT.csv control
// whose base ID exists in the partner CSV but whose full suffixed ID does
// not (audit fix B17b).
//
// Concretely: when CONTEXT references `OPE-001-SOFTWARE` but the partner
// CSV (after suffix mapping) only produced `OPE-001`, the partner submitted
// the row but our suffix allowlist failed to upgrade it — meaning the
// validator would silently drop that control from the run. The warning is
// load-bearing because the loss is invisible otherwise.
//
// Inputs:
//   - contextIDs: every controlId from CONTEXT.csv (suffixed forms included).
//   - partnerIDs: every controlId emitted by internal/excel.ExtractResponses
//     into partner_responses.csv after suffix mapping.
//
// The function does no I/O and does not log; callers wire it to slog at
// validation startup.
func SuffixDriftWarnings(contextIDs, partnerIDs []string) []string {
	partnerSet := make(map[string]struct{}, len(partnerIDs))
	for _, id := range partnerIDs {
		partnerSet[id] = struct{}{}
	}

	// Index partner IDs by their stripped base for the "base exists but
	// suffixed form does not" lookup.
	partnerBaseSet := make(map[string]struct{}, len(partnerIDs))
	for _, id := range partnerIDs {
		partnerBaseSet[stripSuffix(id)] = struct{}{}
	}

	var warnings []string
	seen := make(map[string]struct{})

	for _, ctxID := range contextIDs {
		if _, dup := seen[ctxID]; dup {
			continue
		}
		seen[ctxID] = struct{}{}

		base := stripSuffix(ctxID)
		if base == ctxID {
			continue // not a suffixed control — drift check N/A
		}
		if _, ok := partnerSet[ctxID]; ok {
			continue // partner CSV already produced the suffixed form
		}
		if _, ok := partnerBaseSet[base]; !ok {
			continue // partner didn't submit this control at all — that's not drift
		}

		warnings = append(warnings, fmt.Sprintf(
			"control %s referenced in CONTEXT.csv but not produced by partner CSV after suffix mapping; check SuffixControls list for missing entry",
			ctxID))
	}
	return warnings
}
