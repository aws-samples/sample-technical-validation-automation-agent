package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"thor-golang/internal/controls"
	"thor-golang/internal/runlog"
	"thor-golang/internal/validator"
)

// DefaultModelID is the Bedrock model used for both mapper passes.
// Sonnet 4.5 was chosen over Haiku because the rubric quality
// (per-control prompt_context guidance is dense and overlapping) matters
// for assignment accuracy, and the mapper runs once per partner folder
// — the dominant cost is still validation, not mapping.
const DefaultModelID = "global.anthropic.claude-sonnet-4-5-20250929-v1:0"

// DefaultConcurrency caps in-flight Converse calls during pass 1.
// One call per file: at typical partner-folder sizes (10-30 files) this
// is the regional-RPS-friendly ceiling.
const DefaultConcurrency = 6

// DefaultPerCallTimeout is the wall-clock budget for a single mapper
// Converse call. Tighter than the validator's 120s because the mapper's
// expected output is short JSON, not a reasoning paragraph.
const DefaultPerCallTimeout = 90 * time.Second

// MaxOutputTokens caps the mapper response. The expected payload is a
// short JSON list with rank/confidence (~150 tokens) plus a brief
// rationale; 1500 leaves comfortable headroom.
const MaxOutputTokens = 1500

// CoverageRepairMaxFiles bounds how many files pass-2 may attach per
// empty control. 3 keeps the validator under the 5-doc cap with room
// for the partner-response context.
const CoverageRepairMaxFiles = 3

// BedrockConverseAPI is the subset of the bedrockruntime client used by
// the mapper. Mirrors validator.BedrockConverseAPI so tests and callers
// can wire one Bedrock client into both subsystems.
type BedrockConverseAPI interface {
	Converse(ctx context.Context, in *bedrockruntime.ConverseInput,
		opts ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

// FileProcessor abstracts the validator's file routing (PPTX text
// extraction, image resize, oversize fallback) so the mapper can build
// the same Bedrock content blocks the validator would. Production
// implementations satisfy this with *validator.Validator.
type FileProcessor interface {
	ProcessFileForEvidence(path string) ([]bedrocktypes.ContentBlock, error)
}

// Options configures BuildEvidenceMap.
type Options struct {
	Bedrock        BedrockConverseAPI
	FileProcessor  FileProcessor
	Logger         *slog.Logger
	ModelID        string
	Concurrency    int
	PerCallTimeout time.Duration
	ThorVersion    string

	// DeclaredControls is the controlId set the partner asserted in
	// their self-assessment (already suffix-mapped). The mapper will
	// ONLY assign files to entries in this set — declared-driven
	// behaviour. If empty, BuildEvidenceMap returns an error: every
	// flow must supply a self-assessment first.
	DeclaredControls []string
}

// scoredAssignment captures a single (controlId, file, confidence) tuple
// the mapper produced. Used to build the rank-ordered Map.Controls.
type scoredAssignment struct {
	controlID  string
	file       string
	confidence float64
}

// fileRationale holds pass-1 output for a single file. The rationale is
// reused in pass-2 (coverage repair) so we don't have to re-send the
// document bytes.
type fileRationale struct {
	basename  string
	rationale string
	scores    []scoredAssignment
}

// BuildEvidenceMap runs the two-pass mapper:
//
//	pass 1 — for each supporting_docs file, send its content + the
//	         catalog (filtered to declared controls) and parse a ranked
//	         control assignment.
//	pass 2 — for each declared control still empty, send filenames +
//	         pass-1 rationales (no doc bytes) and ask which 1-3 files
//	         best fit.
//
// Hallucinated control IDs (not in DeclaredControls) are dropped with a
// warning. The mapper does not write to disk — call SaveMap to persist.
func BuildEvidenceMap(ctx context.Context, partnerFolder string, opts Options) (*Map, error) {
	if opts.Bedrock == nil {
		return nil, fmt.Errorf("evidence: BuildEvidenceMap requires Options.Bedrock")
	}
	if opts.FileProcessor == nil {
		return nil, fmt.Errorf("evidence: BuildEvidenceMap requires Options.FileProcessor")
	}
	if len(opts.DeclaredControls) == 0 {
		return nil, fmt.Errorf("evidence: BuildEvidenceMap requires Options.DeclaredControls (none declared)")
	}
	if opts.ModelID == "" {
		opts.ModelID = DefaultModelID
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultConcurrency
	}
	if opts.PerCallTimeout <= 0 {
		opts.PerCallTimeout = DefaultPerCallTimeout
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	// Build the declared-controls catalog. We pull every row from
	// CONTEXT.csv but keep only rubrics for IDs the partner declared.
	allRows, err := controls.Load()
	if err != nil {
		return nil, fmt.Errorf("evidence: load controls: %w", err)
	}
	rubrics := make(map[string]string, len(allRows))
	for _, r := range allRows {
		rubrics[r.ID] = r.PromptContext
	}

	declaredSet := make(map[string]struct{}, len(opts.DeclaredControls))
	declaredOrdered := make([]string, 0, len(opts.DeclaredControls))
	declaredCatalog := make([]controls.Control, 0, len(opts.DeclaredControls))
	for _, id := range opts.DeclaredControls {
		if _, dup := declaredSet[id]; dup {
			continue
		}
		ctxText, ok := rubrics[id]
		if !ok {
			// Declared-but-unknown — skip with a warning. The validator
			// runs SuffixDriftWarnings against the same data, so the
			// operator sees the drift surfaced loudly upstream too.
			opts.Logger.Warn("evidence: declared control not in CONTEXT.csv; skipping",
				slog.String("control_id", id))
			continue
		}
		declaredSet[id] = struct{}{}
		declaredOrdered = append(declaredOrdered, id)
		declaredCatalog = append(declaredCatalog, controls.Control{ID: id, PromptContext: ctxText})
	}
	if len(declaredCatalog) == 0 {
		return nil, fmt.Errorf("evidence: declared controls not found in CONTEXT.csv (drift?)")
	}

	systemPrompt := buildPass1SystemPrompt(declaredCatalog)

	supportingDir := filepath.Join(partnerFolder, "supporting_docs")
	files, err := validator.ListEvidenceFiles(supportingDir)
	if err != nil {
		return nil, fmt.Errorf("evidence: enumerate supporting_docs: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("evidence: no files under %s", supportingDir)
	}

	hash, err := runlog.HashPartnerFolder(partnerFolder)
	if err != nil {
		return nil, fmt.Errorf("evidence: hash partner folder: %w", err)
	}

	rationales, unmapped := runPass1(ctx, opts, systemPrompt, files, declaredSet)

	// Aggregate pass-1 results into rank-ordered control->files lists.
	out := &Map{
		SchemaVersion:     SchemaVersion,
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		PartnerFolderHash: hash,
		ModelID:           opts.ModelID,
		ThorVersion:       opts.ThorVersion,
		DeclaredControls:  declaredOrdered,
		Controls:          make(map[string][]string),
		Files:             make(map[string][]string, len(files)),
	}
	out.Unmapped = append(out.Unmapped, unmapped...)
	sort.Strings(out.Unmapped)

	rankAssignments := make(map[string][]scoredAssignment, len(declaredCatalog))
	for _, fr := range rationales {
		// Per-file ordered list (Files[]) preserves pass-1 order.
		fileOrder := make([]string, 0, len(fr.scores))
		for _, a := range fr.scores {
			fileOrder = append(fileOrder, a.controlID)
			rankAssignments[a.controlID] = append(rankAssignments[a.controlID], a)
		}
		out.Files[fr.basename] = fileOrder
	}

	// Sort each control's assignment list by descending confidence,
	// breaking ties on filename for determinism.
	for id, list := range rankAssignments {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].confidence != list[j].confidence {
				return list[i].confidence > list[j].confidence
			}
			return list[i].file < list[j].file
		})
		ordered := make([]string, 0, len(list))
		for _, a := range list {
			ordered = append(ordered, a.file)
		}
		out.Controls[id] = ordered
	}

	// Pass 2: coverage repair for declared controls still empty.
	missing := make([]string, 0)
	for _, id := range declaredOrdered {
		if len(out.Controls[id]) == 0 {
			missing = append(missing, id)
		}
	}

	if len(missing) > 0 {
		opts.Logger.Info("evidence: pass-2 coverage repair starting",
			slog.Int("missing_count", len(missing)))
		repaired := runPass2(ctx, opts, rubrics, rationales, missing)
		for id, picks := range repaired {
			if len(picks) == 0 {
				continue
			}
			out.Controls[id] = picks
			for _, name := range picks {
				out.Files[name] = appendUnique(out.Files[name], id)
			}
		}
	}

	// EmptyControls = declared controls with no files after both passes.
	for _, id := range declaredOrdered {
		if len(out.Controls[id]) == 0 {
			out.EmptyControls = append(out.EmptyControls, id)
		}
	}

	// Stable order for review/diagnostic output. Files lists keep their
	// pass-1 order; Controls already ranked above.
	for f, list := range out.Files {
		// Deduplicate while preserving order.
		seen := make(map[string]struct{}, len(list))
		clean := make([]string, 0, len(list))
		for _, id := range list {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			clean = append(clean, id)
		}
		out.Files[f] = clean
	}

	return out, nil
}

// runPass1 dispatches one Converse call per file and aggregates the
// per-file rationales. Failures are isolated: a file that errors lands
// in Unmapped and the rest of the mapping continues.
func runPass1(ctx context.Context, opts Options, systemPrompt string,
	files []string, declared map[string]struct{}) ([]fileRationale, []string) {

	type result struct {
		fr  fileRationale
		err error
	}
	results := make([]result, len(files))

	sem := semaphore.NewWeighted(int64(opts.Concurrency))
	g, gctx := errgroup.WithContext(ctx)
	var logMu sync.Mutex

	for i, path := range files {
		idx, p := i, path
		if err := sem.Acquire(gctx, 1); err != nil {
			break
		}
		g.Go(func() error {
			defer sem.Release(1)
			base := filepath.Base(p)
			fr, err := mapOneFile(gctx, opts, systemPrompt, p, declared)
			results[idx] = result{fr: fr, err: err}

			logMu.Lock()
			if err != nil {
				opts.Logger.Warn("evidence: pass-1 file mapping failed",
					slog.String("file", base), slog.String("err", err.Error()))
			} else {
				opts.Logger.Info("evidence: pass-1 file mapped",
					slog.String("file", base),
					slog.Int("control_count", len(fr.scores)))
			}
			logMu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	var rats []fileRationale
	var unmapped []string
	for i, r := range results {
		base := filepath.Base(files[i])
		if r.err != nil {
			unmapped = append(unmapped, base)
			continue
		}
		if len(r.fr.scores) == 0 {
			unmapped = append(unmapped, base)
			// Still include the rationale so pass-2 can reference it.
			r.fr.basename = base
		}
		if r.fr.basename == "" {
			r.fr.basename = base
		}
		rats = append(rats, r.fr)
	}
	return rats, unmapped
}

// mapOneFile sends one supporting_docs file to Bedrock and parses the
// returned rank-ordered control list. Unknown IDs (not in `declared`)
// are dropped silently.
func mapOneFile(ctx context.Context, opts Options, systemPrompt string,
	path string, declared map[string]struct{}) (fileRationale, error) {

	base := filepath.Base(path)
	blocks, err := opts.FileProcessor.ProcessFileForEvidence(path)
	if err != nil {
		return fileRationale{basename: base}, fmt.Errorf("process file: %w", err)
	}
	if len(blocks) == 0 {
		// Empty / unsupported / skipped → no controls assigned.
		return fileRationale{basename: base}, nil
	}

	userBlocks := make([]bedrocktypes.ContentBlock, 0, len(blocks)+2)
	userBlocks = append(userBlocks, &bedrocktypes.ContentBlockMemberText{
		Value: fmt.Sprintf("Filename: %s\n\nDocument contents follow.", base),
	})
	userBlocks = append(userBlocks, blocks...)
	userBlocks = append(userBlocks, &bedrocktypes.ContentBlockMemberText{
		Value: "Output ONLY the JSON object as instructed in the system prompt. Do not include prose.",
	})

	callCtx, cancel := context.WithTimeout(ctx, opts.PerCallTimeout)
	defer cancel()

	out, err := opts.Bedrock.Converse(callCtx, &bedrockruntime.ConverseInput{
		ModelId:  aws.String(opts.ModelID),
		System:   []bedrocktypes.SystemContentBlock{&bedrocktypes.SystemContentBlockMemberText{Value: systemPrompt}},
		Messages: []bedrocktypes.Message{{Role: bedrocktypes.ConversationRoleUser, Content: userBlocks}},
		InferenceConfig: &bedrocktypes.InferenceConfiguration{
			MaxTokens:   aws.Int32(MaxOutputTokens),
			Temperature: aws.Float32(0.0),
		},
	})
	if err != nil {
		return fileRationale{basename: base}, fmt.Errorf("bedrock converse: %w", err)
	}

	raw := extractAssistantText(out)
	if raw == "" {
		return fileRationale{basename: base}, fmt.Errorf("empty bedrock response")
	}

	parsed, err := parsePass1Response(raw)
	if err != nil {
		return fileRationale{basename: base}, err
	}

	scores := make([]scoredAssignment, 0, len(parsed.Assignments))
	seen := make(map[string]struct{}, len(parsed.Assignments))
	for _, a := range parsed.Assignments {
		id := strings.TrimSpace(a.ControlID)
		if id == "" {
			continue
		}
		if _, ok := declared[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		conf := a.Confidence
		if conf < 0 {
			conf = 0
		}
		if conf > 1 {
			conf = 1
		}
		scores = append(scores, scoredAssignment{
			controlID:  id,
			file:       base,
			confidence: conf,
		})
	}
	// Sort by descending confidence so the per-file Files[] list is
	// itself rank-ordered for review output.
	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].confidence > scores[j].confidence
	})
	return fileRationale{
		basename:  base,
		rationale: strings.TrimSpace(parsed.Rationale),
		scores:    scores,
	}, nil
}

// runPass2 asks Bedrock to fill empty declared controls using only
// filenames + pass-1 rationales. No document bytes are re-sent. Returns
// controlId -> ranked file list (capped at CoverageRepairMaxFiles).
func runPass2(ctx context.Context, opts Options, rubrics map[string]string,
	rationales []fileRationale, missing []string) map[string][]string {

	if len(rationales) == 0 || len(missing) == 0 {
		return nil
	}

	systemPrompt := buildPass2SystemPrompt()
	out := make(map[string][]string, len(missing))

	sem := semaphore.NewWeighted(int64(opts.Concurrency))
	g, gctx := errgroup.WithContext(ctx)
	var mu sync.Mutex

	for _, id := range missing {
		controlID := id
		rubric, ok := rubrics[controlID]
		if !ok {
			continue
		}
		if err := sem.Acquire(gctx, 1); err != nil {
			break
		}
		g.Go(func() error {
			defer sem.Release(1)
			picks, err := repairOneControl(gctx, opts, systemPrompt, controlID, rubric, rationales)
			if err != nil {
				opts.Logger.Warn("evidence: pass-2 repair failed",
					slog.String("control_id", controlID),
					slog.String("err", err.Error()))
				return nil
			}
			// Validate picks against the actual rationale set.
			valid := make([]string, 0, len(picks))
			known := make(map[string]struct{}, len(rationales))
			for _, fr := range rationales {
				known[fr.basename] = struct{}{}
			}
			for _, p := range picks {
				if _, ok := known[p]; ok {
					valid = append(valid, p)
				}
			}
			if len(valid) > CoverageRepairMaxFiles {
				valid = valid[:CoverageRepairMaxFiles]
			}
			mu.Lock()
			out[controlID] = valid
			mu.Unlock()
			opts.Logger.Info("evidence: pass-2 repair",
				slog.String("control_id", controlID),
				slog.Int("picked", len(valid)))
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// repairOneControl issues a single Converse call with no document bytes
// — only the control rubric and the pass-1 rationale corpus.
func repairOneControl(ctx context.Context, opts Options, systemPrompt,
	controlID, rubric string, rationales []fileRationale) ([]string, error) {

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Control: %s\nRubric: %s\n\n", controlID, rubric))
	b.WriteString("Candidate files (filename — pass-1 rationale):\n")
	for _, fr := range rationales {
		rationale := fr.rationale
		if rationale == "" {
			rationale = "(no rationale recorded)"
		}
		// One line per file; truncate rationales that are absurdly long.
		fmt.Fprintf(&b, "- %s — %s\n", fr.basename, truncate(rationale, 400))
	}
	b.WriteString("\nReturn JSON only.")

	callCtx, cancel := context.WithTimeout(ctx, opts.PerCallTimeout)
	defer cancel()

	resp, err := opts.Bedrock.Converse(callCtx, &bedrockruntime.ConverseInput{
		ModelId:  aws.String(opts.ModelID),
		System:   []bedrocktypes.SystemContentBlock{&bedrocktypes.SystemContentBlockMemberText{Value: systemPrompt}},
		Messages: []bedrocktypes.Message{{Role: bedrocktypes.ConversationRoleUser, Content: []bedrocktypes.ContentBlock{
			&bedrocktypes.ContentBlockMemberText{Value: b.String()},
		}}},
		InferenceConfig: &bedrocktypes.InferenceConfiguration{
			MaxTokens:   aws.Int32(500),
			Temperature: aws.Float32(0.0),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock converse: %w", err)
	}
	raw := extractAssistantText(resp)
	if raw == "" {
		return nil, fmt.Errorf("empty bedrock response")
	}
	return parsePass2Response(raw)
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// jsonObjectRE matches the first balanced-looking JSON object in a
// Bedrock response. Sonnet sometimes wraps JSON in markdown code fences
// or a one-line preamble; we extract the object regardless.
var jsonObjectRE = regexp.MustCompile(`(?s)\{.*\}`)

// pass1Response is the structured payload pass 1 expects back.
type pass1Response struct {
	Assignments []struct {
		ControlID  string  `json:"controlId"`
		Confidence float64 `json:"confidence"`
	} `json:"assignments"`
	Rationale string `json:"rationale"`
}

func parsePass1Response(raw string) (pass1Response, error) {
	var out pass1Response
	if err := json.Unmarshal([]byte(raw), &out); err == nil && out.Assignments != nil {
		return out, nil
	}
	// Try the legacy single-array form for forward-compat tests.
	var legacy struct {
		ControlIDs []string `json:"controlIds"`
		Rationale  string   `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(raw), &legacy); err == nil && legacy.ControlIDs != nil {
		out.Rationale = legacy.Rationale
		for _, id := range legacy.ControlIDs {
			out.Assignments = append(out.Assignments, struct {
				ControlID  string  `json:"controlId"`
				Confidence float64 `json:"confidence"`
			}{ControlID: id, Confidence: 0.5})
		}
		return out, nil
	}

	match := jsonObjectRE.FindString(raw)
	if match == "" {
		return out, fmt.Errorf("no JSON object in response: %q", truncate(raw, 200))
	}
	if err := json.Unmarshal([]byte(match), &out); err == nil && out.Assignments != nil {
		return out, nil
	}
	if err := json.Unmarshal([]byte(match), &legacy); err == nil && legacy.ControlIDs != nil {
		out.Rationale = legacy.Rationale
		for _, id := range legacy.ControlIDs {
			out.Assignments = append(out.Assignments, struct {
				ControlID  string  `json:"controlId"`
				Confidence float64 `json:"confidence"`
			}{ControlID: id, Confidence: 0.5})
		}
		return out, nil
	}
	return out, fmt.Errorf("parse mapper JSON: raw=%q", truncate(raw, 200))
}

func parsePass2Response(raw string) ([]string, error) {
	var out struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err == nil && out.Files != nil {
		return out.Files, nil
	}
	match := jsonObjectRE.FindString(raw)
	if match == "" {
		return nil, fmt.Errorf("no JSON object in response: %q", truncate(raw, 200))
	}
	if err := json.Unmarshal([]byte(match), &out); err != nil {
		return nil, fmt.Errorf("parse repair JSON: %w (raw=%q)", err, truncate(raw, 200))
	}
	return out.Files, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func extractAssistantText(out *bedrockruntime.ConverseOutput) string {
	if out == nil || out.Output == nil {
		return ""
	}
	msg, ok := out.Output.(*bedrocktypes.ConverseOutputMemberMessage)
	if !ok || msg == nil {
		return ""
	}
	for _, block := range msg.Value.Content {
		if t, ok := block.(*bedrocktypes.ContentBlockMemberText); ok {
			return t.Value
		}
	}
	return ""
}

// buildPass1SystemPrompt assembles the catalog-of-controls system prompt
// for pass 1. The catalog is restricted to declared controls — we never
// invite Bedrock to assign files to controls the partner didn't claim.
//
// The prompt is deliberately verbose: PSA-specific role, explicit
// inclusion rules, an output schema with confidence scoring, and
// concrete examples of relevant vs irrelevant document/control pairs.
func buildPass1SystemPrompt(declared []controls.Control) string {
	var b strings.Builder
	b.WriteString(`You are an evidence-mapping reviewer for the AWS Partner Solution Assessment (PSA) program.

CONTEXT
The AWS Partner Solution Assessment validates that a partner's solution meets AWS competency standards across a fixed set of controls — security, operations, reliability, customer success, and (for AI competencies) responsible-AI controls. Each control has a rubric describing what evidence would satisfy it. A partner submits a self-assessment spreadsheet plus a folder of supporting documents (architecture diagrams, runbooks, customer case studies, security policies, SOWs, marketing decks, screenshots, etc.).

YOUR TASK
You will be shown the contents of ONE supporting document. Identify every control from the catalog below for which this document is reasonable evidence. Score each match with a confidence between 0.0 and 1.0.

DECISION RULES
1. A document is "evidence" for a control when its content addresses the topic the control's rubric evaluates — even partially. Err on inclusion when content overlaps the rubric.
2. Marketing decks, capability summaries, and case studies often map to multiple use-case, customer-example, and capability controls. Treat them generously.
3. Architecture diagrams typically map to documentation, networking-security, reliability, scalability, and operations controls.
4. Production handbooks / runbooks / SOPs map to operational excellence, incident response, change management, and reliability controls.
5. Security policies / IAM diagrams / threat models map to security, identity, and governance controls.
6. SOW templates, project-management artifacts, and onboarding guides map to project-services controls (PRJ, POV, PS).
7. Foundation-model / training-data / responsible-AI documentation maps to GAI* / QCHK* / AGAI* controls if present in the catalog.
8. ONLY use control IDs from the catalog below. Do NOT invent IDs, drop suffixes, or shorten names. The catalog has been pre-filtered to controls this partner declared — anything outside it is wrong by construction.
9. If the document is genuinely off-topic for every catalog entry (e.g. a personal calendar, an unrelated invoice), return an empty assignments array.

CONFIDENCE SCALE
- 0.9-1.0: rubric is the document's primary subject; this is a textbook fit.
- 0.6-0.8: rubric is one of several topics the document addresses; clearly relevant.
- 0.3-0.5: document mentions the topic in passing or covers an adjacent area.
- below 0.3: do not include — the match is too weak.

OUTPUT FORMAT (strict JSON, no prose, no markdown fences):
{
  "assignments": [
    {"controlId": "<exact ID from catalog>", "confidence": <0.0-1.0>}
  ],
  "rationale": "one or two sentences naming the topics the document covers and why those topics matched the listed controls"
}

CATALOG OF DECLARED CONTROLS (controlId | rubric):
`)
	for _, r := range declared {
		ctxText := strings.ReplaceAll(r.PromptContext, "\n", " ")
		b.WriteString(r.ID)
		b.WriteString(" | ")
		b.WriteString(ctxText)
		b.WriteString("\n")
	}
	return b.String()
}

// buildPass2SystemPrompt is the pass-2 "coverage repair" prompt. The
// model receives a single control rubric plus a corpus of (filename,
// pass-1 rationale) pairs and must pick the strongest 1-3 fits.
func buildPass2SystemPrompt() string {
	return `You are completing an evidence map for the AWS Partner Solution Assessment.

A previous pass scanned each supporting document and wrote a one-line rationale describing what it covers. One control was left without any attached files. Given the control's rubric and the rationales for every document, select the 1-3 documents whose rationales best match the rubric.

DECISION RULES
1. Prefer documents whose rationale explicitly names topics the rubric evaluates.
2. If the rubric is a documentation/policy control, prefer SOPs, handbooks, policies, and templates over marketing decks or screenshots.
3. If the rubric is a customer-example control, prefer case studies, customer SOWs, or solution presentations over internal policies.
4. Return at most 3 filenames. Return fewer (or zero) if no rationale plausibly fits.
5. Do NOT invent filenames. Use only filenames from the candidate list, copied verbatim.

OUTPUT FORMAT (strict JSON, no prose):
{"files": ["<filename1>", "<filename2>"]}`
}
