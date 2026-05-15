package controls

// SuffixControls is the canonical list of base control IDs that need a
// `-SERVICE` or `-SOFTWARE` suffix appended to match the Excel template.
//
// Single source of truth (audit fix B29). internal/excel imports this list
// rather than duplicating it. Any future suffix-aware code must import from
// here too — never copy the slice. The list is maintained manually; B17b
// adds a runtime warning when CONTEXT.csv references a suffixed control
// that the partner CSV did not produce, surfacing drift loudly.
var SuffixControls = []string{
	"DOC-001",
	"OPE-001", "OPE-002", "OPE-003",
	"PS-001", "PS-002", "PS-003", "PS-004", "PS-005",
	"REL-001", "REL-002",
	"USE-CASE",
}

// IsSuffixControl reports whether id is one of the controls that needs a
// SERVICE/SOFTWARE suffix.
func IsSuffixControl(id string) bool {
	for _, s := range SuffixControls {
		if s == id {
			return true
		}
	}
	return false
}

// stripSuffix removes a -SERVICE or -SOFTWARE tail; everything else
// (including -CUSTOMER-DEPLOY) is returned unchanged. Mirrors the Python
// chained replace calls in control_category_mapping.get_applicable_controls
// that strip those two suffixes (and only those two).
func stripSuffix(id string) string {
	for _, suffix := range []string{"-SERVICE", "-SOFTWARE"} {
		if n := len(id) - len(suffix); n >= 0 && id[n:] == suffix {
			return id[:n]
		}
	}
	return id
}

// dcControls is the DC_CONTROL_LIST from the Java GenAICompetencyHelper
// (mirrored in server/thor/control_category_mapping.py). These are the only
// controls filtered by designation category — every other control is
// "common" and applies to all categories.
var dcControls = map[string]struct{}{
	// Generative AI Applications
	"GAIAPP-001": {}, "GAIAPP-002": {},
	"GAICRE-001": {}, "GAICRE-002": {}, "GAICRE-003": {}, "GAICRE-004": {}, "GAICRE-005": {},
	// Foundation Models
	"GAIFMS-001": {}, "GAIFMS-002": {}, "GAIFMS-003": {}, "GAIFMS-004": {},
	"GAIFMS-005": {}, "GAIFMS-006": {}, "GAIFMS-007": {}, "GAIFMS-008": {},
	"GAIFMO-001": {},
	// App Development
	"GAIDEV-001": {}, "GAIDEV-002": {},
	// Infrastructure & Data
	"GAIPBHW-001": {}, "GAIPBHW-002": {},
	"GAIDSI-001": {},
	"GAISDG-001": {},
	// Agentic AI
	"QCHK-001": {}, "QCHK-002": {}, "QCHK-003": {}, "QCHK-004": {}, "QCHK-005": {}, "QCHK-006": {},
	"QCHKA-001": {}, "QCHKA-002": {}, "QCHKA-003": {}, "QCHKA-004": {}, "QCHKA-005": {}, "QCHKA-006": {},
	"QCHKT-001": {}, "QCHKT-002": {}, "QCHKT-003": {}, "QCHKT-004": {}, "QCHKT-005": {}, "QCHKT-006": {}, "QCHKT-007": {},
	"AGAIPS-001": {}, "AGAIPS-002": {}, "AGAIPS-003": {}, "AGAIPS-004": {},
	// Consulting
	"GENAICEX-001": {},
	"GENAIPR-001":  {}, "GENAIPR-002": {}, "GENAIPR-003": {},
	"GENAIPR-004": {}, "GENAIPR-005": {}, "GENAIPR-006": {},
}

// categoryControls maps a designation-category name to the DC controls
// that apply to it. Mirrors CATEGORY_CONTROLS in the Python module.
var categoryControls = map[string][]string{
	"Generative AI Applications": {
		"GAIAPP-001", "GAIAPP-002",
		"GAICRE-001", "GAICRE-002", "GAICRE-003", "GAICRE-004", "GAICRE-005",
	},
	"Foundation Models and App Development": {
		"GAIFMS-001", "GAIFMS-002", "GAIFMS-003", "GAIFMS-004",
		"GAIFMS-005", "GAIFMS-006", "GAIFMS-007", "GAIFMS-008",
		"GAIFMO-001",
		"GAIDEV-001", "GAIDEV-002",
		"GAICRE-001", "GAICRE-002", "GAICRE-003", "GAICRE-004", "GAICRE-005",
	},
	"Infrastructure and Data": {
		"GAIPBHW-001", "GAIPBHW-002",
		"GAIDSI-001",
		"GAISDG-001",
		"GAICRE-001", "GAICRE-002", "GAICRE-003", "GAICRE-004", "GAICRE-005",
	},
	"Agentic AI Tools": {
		"QCHKT-001", "QCHKT-002", "QCHKT-003", "QCHKT-004",
		"QCHKT-005", "QCHKT-006", "QCHKT-007",
	},
	"Agentic AI Applications": {
		"QCHKA-001", "QCHKA-002", "QCHKA-003",
		"QCHKA-004", "QCHKA-005", "QCHKA-006",
	},
	"Agentic AI Consulting Services": {
		"QCHK-001", "QCHK-002", "QCHK-003", "QCHK-004", "QCHK-005", "QCHK-006",
		"AGAIPS-001", "AGAIPS-002", "AGAIPS-003", "AGAIPS-004",
	},
	"Generative AI Consulting Services": {
		"GENAICEX-001",
		"GENAIPR-001", "GENAIPR-002", "GENAIPR-003",
		"GENAIPR-004", "GENAIPR-005", "GENAIPR-006",
	},
}

// Categories returns the supported designation-category names.
func Categories() []string {
	out := make([]string, 0, len(categoryControls))
	for k := range categoryControls {
		out = append(out, k)
	}
	return out
}

// GetApplicable filters controlIDs to those that apply to category.
//
// Audit fix B1: returns []string only. The Python equivalent returned
// (applicable, not_applicable) but every caller treated the value as a flat
// list, which silently broke category filtering. Common controls (anything
// whose base ID is not in dcControls) always pass through; category-specific
// controls are kept only if their base ID is in the category's allowlist.
//
// If category is empty or unknown, all controls pass through (conservative
// default — same as the Python behaviour).
func GetApplicable(category string, controlIDs []string) []string {
	if category == "" {
		return append([]string(nil), controlIDs...)
	}
	allowed, ok := categoryControls[category]
	if !ok {
		return append([]string(nil), controlIDs...)
	}

	allowSet := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		allowSet[id] = struct{}{}
	}

	out := make([]string, 0, len(controlIDs))
	for _, id := range controlIDs {
		base := stripSuffix(id)
		if _, isDC := dcControls[base]; !isDC {
			out = append(out, id)
			continue
		}
		if _, ok := allowSet[base]; ok {
			out = append(out, id)
		}
	}
	return out
}
