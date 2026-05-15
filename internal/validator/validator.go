// Package validator drives per-control Bedrock Converse calls,
// orchestrates parallelism + consensus, and returns structured Result
// values that downstream renderers consume without re-parsing.
package validator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"thor-golang/internal/controls"
	"thor-golang/internal/excel"
	"thor-golang/internal/prompts"
)

// DefaultModelID is the Bedrock inference profile Thor targets. Mirrors
// the Python `model_id="global.anthropic.claude-sonnet-4-5-..."` default.
const DefaultModelID = "global.anthropic.claude-sonnet-4-5-20250929-v1:0"

// DefaultConcurrency caps in-flight Converse calls during ValidateBatch.
// Matches Python `ThreadPoolExecutor(max_workers=15)`.
const DefaultConcurrency = 15

// DefaultPerCallTimeout is the wall-clock budget for a single Converse
// call. 180s accommodates large evidence payloads (multi-page PDFs)
// that can take longer for the model to process.
const DefaultPerCallTimeout = 180 * time.Second

// DefaultMaxAttempts is the SDK retry budget. PLAN.md 1E.6 fixes this at 6.
const DefaultMaxAttempts = 6

// BedrockConverseAPI is the subset of the bedrockruntime client used by
// the validator. Tests substitute their own implementation.
type BedrockConverseAPI interface {
	Converse(ctx context.Context, in *bedrockruntime.ConverseInput,
		opts ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

// EvidenceFilter selects which supporting_docs filenames (basenames) a
// given control should see. Returning nil disables filtering for that
// control — every file in supporting_docs is sent. Returning an empty
// slice means "no files" and is honoured (the control runs against the
// partner response only).
//
// The mapper in internal/evidence produces a Map; cli/validate.go wraps
// it in an EvidenceFilter before constructing the Validator.
type EvidenceFilter func(controlID string) (basenames []string, hasMap bool)

// Options configures a Validator. Most fields default sensibly; only
// AWSConfig is required (it builds the Bedrock client unless Bedrock is
// supplied directly).
type Options struct {
	AWSConfig      aws.Config
	Bedrock        BedrockConverseAPI // optional override; when nil, built from AWSConfig
	Marketplace    MarketplaceChecker // optional override (defaults to net/http)
	Logger         *slog.Logger       // defaults to a JSON handler on stderr
	ModelID        string             // defaults to DefaultModelID
	SystemMode     prompts.SystemMode // defaults to prompts.SystemRevised
	Concurrency    int                // defaults to DefaultConcurrency
	PerCallLimit   time.Duration      // defaults to DefaultPerCallTimeout
	EvidenceFilter EvidenceFilter     // optional; per-control file allowlist
}

// Validator is the per-process Bedrock + prompts handle. Goroutine-safe;
// share one across an entire validation run.
type Validator struct {
	bedrock        BedrockConverseAPI
	logger         *slog.Logger
	modelID        string
	systemMode     prompts.SystemMode
	concurrency    int
	perCallTO      time.Duration
	evidenceFilter EvidenceFilter

	systemPrompt []byte
	contextByID  map[string]string

	imageCache  *imageCache
	marketplace MarketplaceChecker
	counters    *Counters

	// specialChecks routes specific control IDs to bespoke handlers that
	// short-circuit Bedrock entirely. The Python implementation hard-codes
	// `if controlID == "DOC-006"` at the same call-site; this map is the
	// equivalent dispatch table. Adding a check to a different control —
	// or applying the same check to multiple — is a single map entry
	// (see init below). The handler signature is intentionally narrow:
	// (controlID, partnerResponse) → Result, with no access to evidence
	// files, so the registry stays declarative.
	specialChecks map[string]func(ctx context.Context, controlID, partnerResp string) Result
}

// NewValidator builds a Validator from Options. Performs no network I/O
// and returns an error only on prompt-loading or config mismatches.
func NewValidator(opts Options) (*Validator, error) {
	if opts.SystemMode == "" {
		opts.SystemMode = prompts.DefaultSystemMode
	}
	if !opts.SystemMode.Valid() {
		return nil, fmt.Errorf("validator: unknown SystemMode %q", opts.SystemMode)
	}
	if opts.ModelID == "" {
		opts.ModelID = DefaultModelID
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultConcurrency
	}
	if opts.PerCallLimit <= 0 {
		opts.PerCallLimit = DefaultPerCallTimeout
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	systemBytes, err := prompts.System(opts.SystemMode)
	if err != nil {
		return nil, err
	}
	contextRows, err := controls.LoadMap()
	if err != nil {
		return nil, err
	}
	contextByID := make(map[string]string, len(contextRows))
	for id, c := range contextRows {
		contextByID[id] = c.PromptContext
	}

	bedrockCli := opts.Bedrock
	if bedrockCli == nil {
		bedrockCli = bedrockruntime.NewFromConfig(opts.AWSConfig, func(o *bedrockruntime.Options) {
			o.RetryMaxAttempts = DefaultMaxAttempts
		})
	}

	mkt := opts.Marketplace
	if mkt == nil {
		mkt = newHTTPMarketplaceChecker()
	}

	v := &Validator{
		bedrock:        bedrockCli,
		logger:         opts.Logger,
		modelID:        opts.ModelID,
		systemMode:     opts.SystemMode,
		concurrency:    opts.Concurrency,
		perCallTO:      opts.PerCallLimit,
		evidenceFilter: opts.EvidenceFilter,
		systemPrompt:   systemBytes,
		contextByID:    contextByID,
		imageCache:     newImageCache(),
		marketplace:    mkt,
		counters:       &Counters{},
	}
	v.specialChecks = map[string]func(context.Context, string, string) Result{
		"DOC-006": v.runMarketplaceCheck,
	}
	return v, nil
}

// filterEvidence applies the configured EvidenceFilter to a controlId's
// candidate file set. When no filter is set, or the filter reports it
// has no map for this control, the unfiltered list is returned (graceful
// fall-back to today's "send everything" behaviour).
//
// When a filter IS set, the returned slice preserves the *filter's*
// order — not the original `files` order. This is important: the
// evidence map's per-control file list is rank-ordered by mapper
// confidence (most relevant first), and downstream consumers
// (planAppendixMerge, planPDFDemotions) treat input order as relevance
// order. Using filter order ensures the top-ranked files keep their
// full-fidelity binary doc-block routing when the 5-doc cap binds.
//
// Files named in the filter but not present on disk are silently
// dropped (matches today's set-intersection behaviour).
func (v *Validator) filterEvidence(controlID string, files []string) []string {
	if v.evidenceFilter == nil {
		return files
	}
	allowed, hasMap := v.evidenceFilter(controlID)
	if !hasMap {
		return files
	}
	pathByBase := make(map[string]string, len(files))
	for _, p := range files {
		pathByBase[filepath.Base(p)] = p
	}
	out := make([]string, 0, len(allowed))
	for _, name := range allowed {
		if p, ok := pathByBase[name]; ok {
			out = append(out, p)
		}
	}
	v.logger.Info("evidence filter applied",
		slog.String("control_id", controlID),
		slog.Int("filtered_count", len(out)),
		slog.Int("total_count", len(files)))
	return out
}

// runMarketplaceCheck wraps MarketplaceChecker.Check, stamping the
// controlID onto the returned Result. Registered against "DOC-006" in
// NewValidator; add additional control IDs there to apply this check
// elsewhere.
func (v *Validator) runMarketplaceCheck(ctx context.Context, controlID, partnerResp string) Result {
	r := v.marketplace.Check(ctx, partnerResp)
	r.ControlID = controlID
	return r
}

// Counters returns a snapshot of the per-validation counters. Read this
// only after the validation batch has joined — internal updates happen
// under the per-call goroutine without locking, matching the way the
// Python implementation populates its own counter dict.
func (v *Validator) Counters() Counters { return *v.counters }

// PartnerResponse looks up a controlId in partner_responses.csv. Returns
// the empty string if the control isn't present (matches Python
// `partner_response = ""` on miss).
func (v *Validator) PartnerResponse(partnerFolder, controlID string) (string, error) {
	rows, err := excel.LoadResponses(filepath.Join(partnerFolder, "partner_responses.csv"))
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		if r.ControlID == controlID {
			return r.PartnerResponse, nil
		}
	}
	return "", nil
}

// ValidateControl runs a single control. The DOC-006 marketplace check
// short-circuits Bedrock entirely.
func (v *Validator) ValidateControl(ctx context.Context, controlID, partnerFolder string) Result {
	partnerResp, err := v.PartnerResponse(partnerFolder, controlID)
	if err != nil {
		return errResult(controlID, err, "load partner_responses.csv")
	}

	if check, ok := v.specialChecks[controlID]; ok {
		return check(ctx, controlID, partnerResp)
	}

	promptContext, ok := v.contextByID[controlID]
	if !ok {
		return errResult(controlID, nil, "control %s not found in CONTEXT.csv", controlID)
	}
	if partnerResp == "" {
		// Python returns the literal string "No partner response found";
		// surface this as a typed FAILED result with that reasoning.
		return Result{
			ControlID: controlID,
			Status:    StatusFailed,
			Reasoning: "No partner response found",
		}
	}

	supportingDir := filepath.Join(partnerFolder, "supporting_docs")
	files, err := listEvidenceFiles(supportingDir)
	if err != nil {
		return errResult(controlID, err, "enumerate supporting_docs")
	}
	files = v.filterEvidence(controlID, files)
	contentBlocks, err := v.processFiles(files)
	if err != nil {
		return errResult(controlID, err, "process supporting_docs")
	}

	// Per-call timeout — does not need to be the full ctx.Done() chain.
	callCtx, cancel := context.WithTimeout(ctx, v.perCallTO)
	defer cancel()

	out, err := v.bedrock.Converse(callCtx, &bedrockruntime.ConverseInput{
		ModelId:  aws.String(v.modelID),
		System:   []bedrocktypes.SystemContentBlock{&bedrocktypes.SystemContentBlockMemberText{Value: string(v.systemPrompt)}},
		Messages: []bedrocktypes.Message{userMessage(partnerResp, controlID, promptContext, contentBlocks)},
		InferenceConfig: &bedrocktypes.InferenceConfiguration{
			MaxTokens:   aws.Int32(2000),
			Temperature: aws.Float32(0.0),
		},
	})
	if err != nil {
		return errResult(controlID, err, "bedrock converse")
	}

	raw := extractAssistantText(out)
	v.logger.Info("control validated",
		slog.String("control_id", controlID),
		slog.String("system_mode", string(v.systemMode)),
		slog.Int64("input_tokens", int64Or0(out.Usage, func(u *bedrocktypes.TokenUsage) *int32 { return u.InputTokens })),
		slog.Int64("output_tokens", int64Or0(out.Usage, func(u *bedrocktypes.TokenUsage) *int32 { return u.OutputTokens })),
	)
	return parseResult(controlID, raw)
}

// userMessage builds the user-side message body in the order Python emits:
// prompt_context, partner-response text, all document/image evidence,
// then the closing question. Audit B6 fix: one Converse call per control,
// no within-control batching.
func userMessage(partnerResp, controlID, promptContext string, evidence []bedrocktypes.ContentBlock) bedrocktypes.Message {
	const closingQuestion = "Based on the details provided, is this offering approved?"
	respText := fmt.Sprintf("Partner Response from self-assessment:\nResponse for %s submitted by Partner: %s",
		controlID, partnerResp)

	blocks := make([]bedrocktypes.ContentBlock, 0, len(evidence)+3)
	blocks = append(blocks,
		&bedrocktypes.ContentBlockMemberText{Value: promptContext},
		&bedrocktypes.ContentBlockMemberText{Value: respText},
	)
	blocks = append(blocks, evidence...)
	blocks = append(blocks, &bedrocktypes.ContentBlockMemberText{Value: closingQuestion})

	return bedrocktypes.Message{Role: bedrocktypes.ConversationRoleUser, Content: blocks}
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

func int64Or0(u *bedrocktypes.TokenUsage, get func(*bedrocktypes.TokenUsage) *int32) int64 {
	if u == nil {
		return 0
	}
	p := get(u)
	if p == nil {
		return 0
	}
	return int64(*p)
}
