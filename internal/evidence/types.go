// Package evidence builds and persists a control->files mapping for a
// partner folder. The mapping is produced by a Bedrock pre-pass driven by
// the partner's self-assessment: for each declared control, the mapper
// picks the supporting_docs files that best evidence the rubric. The
// validator then sends only the relevant subset (rank-ordered) to each
// per-control Converse call.
//
// Two-pass design:
//   - Pass 1 (file -> controls): one Converse call per supporting_docs
//     file, with the catalog filtered to *only* the declared controls
//     from partner_responses.csv. Returns ranked control assignments per
//     file.
//   - Pass 2 (coverage repair): for any declared control still empty
//     after pass 1, ask Bedrock once given just the filenames + their
//     pass-1 rationales which 1-3 files are the strongest fit. Cheap
//     because no document bytes are re-sent.
//
// Why declared-driven: a partner's submission is bounded by what they
// claimed in the Excel checklist. Mapping every file against every
// catalog entry wastes calls and invites hallucinated assignments to
// controls the partner never declared.
package evidence

// SchemaVersion is bumped when the on-disk evidence_map.json format
// changes in a way older readers can't cope with. v2 introduces
// rank-ordered file lists per control and the EmptyControls field.
const SchemaVersion = 2

// MapFileName is the canonical name for the persisted evidence map at
// the partner-folder root. Stored alongside partner_responses.csv so the
// folder remains the single artifact a reviewer needs to audit a run.
const MapFileName = "evidence_map.json"

// Map is the persisted control->files mapping for a partner folder.
//
// Map.Controls[id] is *rank-ordered* (highest relevance first) so the
// validator can honour the mapper's preference when capping the per-call
// document-block count to Bedrock's 5-block limit.
//
// PartnerFolderHash matches runlog.HashPartnerFolder output and is used
// for cache invalidation. SchemaVersion bumps also force a rebuild.
type Map struct {
	SchemaVersion     int    `json:"schemaVersion"`
	GeneratedAt       string `json:"generatedAt"`
	PartnerFolderHash string `json:"partnerFolderHash"`
	ModelID           string `json:"modelId"`
	ThorVersion       string `json:"thorVersion"`

	// DeclaredControls is the controlId set sourced from
	// partner_responses.csv (already suffix-mapped, e.g.
	// "DOC-001-SERVICE"). The mapper only ever assigns files to entries
	// in this set.
	DeclaredControls []string `json:"declaredControls"`

	// Controls maps controlId -> rank-ordered list of supporting_docs
	// basenames. Order is mapper-confidence-descending (highest first).
	Controls map[string][]string `json:"controls"`

	// Files captures the inverse projection (filename -> control IDs the
	// mapper assigned, in the same rank order pass-1 produced). Useful
	// for review and diagnostic output; not load-bearing for the
	// validator.
	Files map[string][]string `json:"files"`

	// Unmapped lists supporting_docs filenames that produced no control
	// assignments — either because they're genuinely off-topic or
	// because pass-1 errored on them. Surfaced as warnings.
	Unmapped []string `json:"unmapped,omitempty"`

	// EmptyControls lists declared controls that, after both mapper
	// passes, still have no files attached. The validator treats these
	// as "send no evidence" — partner response only — which forces a
	// clean FAIL signal when the partner declared a control they have
	// no documentation for.
	EmptyControls []string `json:"emptyControls,omitempty"`
}

// FilesFor returns the supporting_docs filenames mapped to controlID, in
// rank order. Returns an empty slice (never nil) when the control has no
// mapped files.
func (m *Map) FilesFor(controlID string) []string {
	if m == nil || m.Controls == nil {
		return nil
	}
	out := m.Controls[controlID]
	if out == nil {
		return []string{}
	}
	return out
}
