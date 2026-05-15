package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"thor-golang/internal/awsauth"
	"thor-golang/internal/controls"
	"thor-golang/internal/evidence"
	"thor-golang/internal/excel"
	"thor-golang/internal/prompts"
	"thor-golang/internal/report"
	"thor-golang/internal/runlog"
	"thor-golang/internal/validator"
	"thor-golang/internal/version"
)

// ValidatorFactory builds a *validator.Validator. Tests inject a fake
// (with a stubbed Bedrock client) so CallTool() doesn't hit AWS. The
// optional filter is the evidence map filter consumed during validation;
// pass nil to disable per-control filtering.
type ValidatorFactory func(ctx context.Context, mode prompts.SystemMode, concurrency int,
	filter validator.EvidenceFilter) (*validator.Validator, error)

// ProductionValidatorFactory uses awsauth + the live Bedrock SDK. Returned
// from Options when nothing else is supplied.
var ProductionValidatorFactory ValidatorFactory = func(ctx context.Context, mode prompts.SystemMode, concurrency int,
	filter validator.EvidenceFilter) (*validator.Validator, error) {
	cfg, err := awsauth.LoadConfig(ctx, os.Getenv("AWS_REGION"), os.Getenv("AWS_PROFILE"))
	if err != nil {
		return nil, err
	}
	return validator.NewValidator(validator.Options{
		AWSConfig:      cfg,
		SystemMode:     mode,
		Concurrency:    concurrency,
		EvidenceFilter: filter,
	})
}

// ------------------------------------------------------------------
// Tool input structs. The MCP SDK auto-generates JSON schemas from these.
// ------------------------------------------------------------------

type doctorInput struct{}

type convertInput struct {
	PartnerFolder string `json:"partner_folder"`
	AppType       string `json:"app_type,omitempty"`
}

type validateInput struct {
	PartnerFolder  string `json:"partner_folder"`
	Controls       string `json:"controls,omitempty"`
	Category       string `json:"category,omitempty"`
	AppType        string `json:"app_type,omitempty"`
	Consensus      int    `json:"consensus,omitempty"`
	Concurrency    int    `json:"concurrency,omitempty"`
	SkipConversion bool   `json:"skip_conversion,omitempty"`
	NoMap          bool   `json:"no_map,omitempty"`
	SystemMode     string `json:"system_mode,omitempty"`
}

type mapInput struct {
	PartnerFolder string `json:"partner_folder"`
	Concurrency   int    `json:"concurrency,omitempty"`
	ModelID       string `json:"model_id,omitempty"`
	AppType       string `json:"app_type,omitempty"`
}

type diffInput struct {
	PartnerFolder string `json:"partner_folder"`
	Mode          string `json:"mode,omitempty"`
	Run1          string `json:"run1,omitempty"`
	Run2          string `json:"run2,omitempty"`
}

type prepInput struct {
	PartnerName string `json:"partner_name"`
	Category    string `json:"category"`
	AppType     string `json:"app_type,omitempty"`
	Folder      string `json:"folder,omitempty"`
}

type listControlsInput struct {
	Category string `json:"category,omitempty"`
}

type exportInput struct {
	PartnerFolder string `json:"partner_folder"`
	Report        string `json:"report,omitempty"`
}

// ------------------------------------------------------------------
// Tool registration.
// ------------------------------------------------------------------

// registerTools wires all 7 tools. Each handler is intentionally narrow:
// parse args -> call internal/* -> return TextContent. Audit decision in
// PLAN.md 3.2 drops `thor_run` (Python implementation depends on a
// non-existent module).
func registerTools(s *mcp.Server, opts Options, logger *slog.Logger) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "thor_doctor",
		Description: "Diagnose AWS credentials, Bedrock model access, and embedded prompts.",
	}, makeHandler(logger, "thor_doctor", handleDoctor))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "thor_convert",
		Description: "Extract partner_responses.csv from the partner Excel checklist.",
	}, makeHandler(logger, "thor_convert", handleConvert))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "thor_validate",
		Description: "Validate a partner folder against AWS PSA controls.",
	}, makeHandler(logger, "thor_validate", makeValidateHandler(opts.Validator)))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "thor_diff",
		Description: "Compare two validation runs (or list a timeline across all runs).",
	}, makeHandler(logger, "thor_diff", handleDiff))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "thor_prep",
		Description: "Create a partner folder skeleton ready for evidence.",
	}, makeHandler(logger, "thor_prep", handlePrep))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "thor_list_controls",
		Description: "List the controls Thor knows about (optionally filtered by --category).",
	}, makeHandler(logger, "thor_list_controls", handleListControls))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "thor_export",
		Description: "Render a validation_summary.md to a self-contained HTML file.",
	}, makeHandler(logger, "thor_export", handleExport))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "thor_map",
		Description: "Build the control→files evidence map (writes evidence_map.json). Auto-built by thor_validate when missing or stale.",
	}, makeHandler(logger, "thor_map", makeMapHandler(opts.Validator)))
}

// makeHandler wraps a typed handler with the boilerplate every tool needs:
// a per-call structured log line and TextContent result formatting.
//
// The MCP SDK's ToolHandlerFor is generic; ours always emits a structured
// "no-output" Out type because every Thor tool returns free-form text.
type emptyOutput struct{}

func makeHandler[In any](
	logger *slog.Logger,
	name string,
	fn func(ctx context.Context, in In) (string, error),
) mcp.ToolHandlerFor[In, emptyOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, emptyOutput, error) {
		start := time.Now()
		text, err := fn(ctx, in)
		latency := time.Since(start)
		if err != nil {
			logger.Error("tool failed", "tool", name, "latency_ms", latency.Milliseconds(), "err", err.Error())
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, emptyOutput{}, nil
		}
		logger.Info("tool ok", "tool", name, "latency_ms", latency.Milliseconds(), "bytes_out", len(text))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, emptyOutput{}, nil
	}
}

// ------------------------------------------------------------------
// Per-tool handlers.
// ------------------------------------------------------------------

func handleDoctor(ctx context.Context, _ doctorInput) (string, error) {
	cfg, err := awsauth.LoadConfig(ctx, os.Getenv("AWS_REGION"), os.Getenv("AWS_PROFILE"))
	if err != nil {
		return "", err
	}
	rep := awsauth.Diagnose(ctx, cfg, os.Getenv("AWS_PROFILE"))

	type out struct {
		ServerVersion   string                   `json:"serverVersion"`
		EmbeddedPrompts bool                     `json:"embeddedPromptsOK"`
		AWS             awsauth.DiagnosticReport `json:"aws"`
		Errors          []string                 `json:"errors,omitempty"`
	}
	embedded := embeddedPromptsOK()
	errs := make([]string, 0, len(rep.Errors))
	for _, e := range rep.Errors {
		errs = append(errs, e.Error())
	}
	body, err := json.MarshalIndent(out{
		ServerVersion:   version.Short(),
		EmbeddedPrompts: embedded,
		AWS:             rep,
		Errors:          errs,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func embeddedPromptsOK() bool {
	for _, m := range []prompts.SystemMode{prompts.SystemOld, prompts.SystemNew, prompts.SystemRevised} {
		b, err := prompts.System(m)
		if err != nil || len(b) == 0 {
			return false
		}
	}
	b, err := prompts.ContextCSV()
	return err == nil && len(b) > 0
}

func handleConvert(_ context.Context, in convertInput) (string, error) {
	if in.PartnerFolder == "" {
		return "", fmt.Errorf("partner_folder is required")
	}
	t, err := parseAppType(in.AppType, excel.AppTypeSoftware)
	if err != nil {
		return "", err
	}
	xlsx, err := findExcelFile(in.PartnerFolder)
	if err != nil {
		return "", err
	}
	out := filepath.Join(in.PartnerFolder, "partner_responses.csv")
	if err := excel.ExtractResponses(xlsx, out, t, nil); err != nil {
		return "", err
	}
	rows, err := excel.LoadResponses(out)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Extracted %d response(s) from %s -> %s", len(rows), xlsx, out), nil
}

func makeValidateHandler(factory ValidatorFactory) func(context.Context, validateInput) (string, error) {
	return func(ctx context.Context, in validateInput) (string, error) {
		if in.PartnerFolder == "" {
			return "", fmt.Errorf("partner_folder is required")
		}
		mode := prompts.SystemMode(strings.ToLower(in.SystemMode))
		if mode == "" {
			mode = prompts.DefaultSystemMode
		}
		if !mode.Valid() {
			return "", fmt.Errorf("invalid system_mode %q", in.SystemMode)
		}
		concurrency := in.Concurrency
		if concurrency <= 0 {
			concurrency = validator.DefaultConcurrency
		}
		consensus := in.Consensus
		if consensus <= 0 {
			consensus = 1
		}

		// Optional conversion step.
		if !in.SkipConversion {
			t, err := parseAppType(in.AppType, excel.AppTypeSoftware)
			if err != nil {
				return "", err
			}
			if xlsx, err := findExcelFile(in.PartnerFolder); err == nil {
				out := filepath.Join(in.PartnerFolder, "partner_responses.csv")
				if err := excel.ExtractResponses(xlsx, out, t, nil); err != nil {
					return "", fmt.Errorf("convert: %w", err)
				}
			} else if _, statErr := os.Stat(filepath.Join(in.PartnerFolder, "partner_responses.csv")); statErr != nil {
				return "", err
			}
		}

		csvPath := filepath.Join(in.PartnerFolder, "partner_responses.csv")
		rows, err := excel.LoadResponses(csvPath)
		if err != nil {
			return "", err
		}
		ids := uniqueResponseIDs(rows)

		ctxRows, err := controls.LoadMap()
		if err != nil {
			return "", err
		}
		valid := make(map[string]struct{}, len(ctxRows))
		for id := range ctxRows {
			valid[id] = struct{}{}
		}
		filtered := make([]string, 0, len(ids))
		for _, id := range ids {
			if _, ok := valid[id]; ok {
				filtered = append(filtered, id)
			}
		}

		var controlIDs []string
		switch {
		case in.Controls != "":
			controlIDs = strings.Fields(in.Controls)
		case in.Category != "":
			controlIDs = controls.GetApplicable(in.Category, filtered)
		default:
			controlIDs = filtered
		}
		if len(controlIDs) == 0 {
			return "", fmt.Errorf("no controls to validate")
		}

		var filter validator.EvidenceFilter
		if !in.NoMap {
			m, err := ensureEvidenceMapMCP(ctx, in.PartnerFolder, filtered, factory, mode, concurrency)
			if err != nil {
				if errors.Is(err, awsauth.ErrExpiredCredentials) {
					return "", err
				}
				// Soft-fail: log via the returned summary and continue
				// without a filter (today's behaviour).
				fmt.Fprintf(os.Stderr,
					"WARN: evidence map unavailable (%v); falling back to all-files-per-control\n", err)
			} else if m != nil {
				filter = mapFilterMCP(m)
			}
		}

		v, err := factory(ctx, mode, concurrency, filter)
		if err != nil {
			return "", err
		}

		startedAt := time.Now().UTC()
		results, err := v.ValidateBatchWithFallback(ctx, controlIDs, in.PartnerFolder, validator.BatchOptions{
			Concurrency:   concurrency,
			ConsensusRuns: consensus,
		})
		if err != nil {
			return "", err
		}
		completedAt := time.Now().UTC()

		if err := writeReports(in.PartnerFolder, controlIDs, results, mode, startedAt, completedAt); err != nil {
			return "", err
		}

		passed, failed, waived, errored := countByStatus(results)
		return fmt.Sprintf("Validated %d control(s): passed=%d failed=%d waived=%d errored=%d",
			len(controlIDs), passed, failed, waived, errored), nil
	}
}

func handleDiff(_ context.Context, in diffInput) (string, error) {
	if in.PartnerFolder == "" {
		return "", fmt.Errorf("partner_folder is required")
	}
	reports, err := report.ListReports(in.PartnerFolder)
	if err != nil {
		return "", err
	}
	switch in.Mode {
	case "all":
		return report.RenderTimeline(filepath.Base(in.PartnerFolder), reports)
	case "custom":
		r1, r2, err := pickCustomReports(reports, in.Run1, in.Run2)
		if err != nil {
			return "", err
		}
		return diffBody(in.PartnerFolder, r1, r2)
	default:
		if len(reports) < 2 {
			return "", fmt.Errorf("need at least 2 validation reports to diff; found %d", len(reports))
		}
		return diffBody(in.PartnerFolder, reports[len(reports)-2], reports[len(reports)-1])
	}
}

func diffBody(folder string, r1, r2 report.ReportFile) (string, error) {
	res1, err := report.ParseSummary(r1.Path)
	if err != nil {
		return "", err
	}
	res2, err := report.ParseSummary(r2.Path)
	if err != nil {
		return "", err
	}
	return report.RenderDiff(filepath.Base(folder), r1.Timestamp, r2.Timestamp, res1, res2), nil
}

func pickCustomReports(reports []report.ReportFile, run1, run2 string) (report.ReportFile, report.ReportFile, error) {
	if len(reports) == 0 {
		return report.ReportFile{}, report.ReportFile{}, fmt.Errorf("no reports available")
	}
	first := report.FindReport(reports, run1)
	second := report.FindReport(reports, run2)
	if run1 != "" && first == nil {
		return report.ReportFile{}, report.ReportFile{}, fmt.Errorf("no report matching %q", run1)
	}
	if run2 != "" && second == nil {
		return report.ReportFile{}, report.ReportFile{}, fmt.Errorf("no report matching %q", run2)
	}
	if first == nil {
		first = &reports[0]
	}
	if second == nil {
		second = &reports[len(reports)-1]
	}
	return *first, *second, nil
}

func handlePrep(_ context.Context, in prepInput) (string, error) {
	if in.PartnerName == "" {
		return "", fmt.Errorf("partner_name is required")
	}
	if in.Category == "" {
		return "", fmt.Errorf("category is required")
	}
	t, err := parseAppType(in.AppType, excel.AppTypeSoftware)
	if err != nil {
		return "", err
	}
	base := in.Folder
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, "Documents", "PSA_Validations")
	}
	partnerFolder := filepath.Join(base, in.PartnerName)
	for _, p := range []string{
		partnerFolder,
		filepath.Join(partnerFolder, "supporting_docs"),
		filepath.Join(partnerFolder, "reports", "summary"),
	} {
		if err := os.MkdirAll(p, 0o750); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", p, err)
		}
	}

	rows, err := controls.Load()
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	applicable := controls.GetApplicable(in.Category, ids)
	return fmt.Sprintf("Created %s — category=%s, app_type=%s, applicable=%d",
		partnerFolder, in.Category, t, len(applicable)), nil
}

func handleListControls(_ context.Context, in listControlsInput) (string, error) {
	rows, err := controls.Load()
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if in.Category != "" {
		ids = controls.GetApplicable(in.Category, ids)
	}
	var b strings.Builder
	if in.Category != "" {
		fmt.Fprintf(&b, "# Controls for: %s (%d)\n", in.Category, len(ids))
	} else {
		fmt.Fprintf(&b, "# All Controls (%d)\n", len(ids))
	}
	for _, id := range ids {
		fmt.Fprintf(&b, "- %s\n", id)
	}
	return b.String(), nil
}

func makeMapHandler(factory ValidatorFactory) func(context.Context, mapInput) (string, error) {
	return func(ctx context.Context, in mapInput) (string, error) {
		if in.PartnerFolder == "" {
			return "", fmt.Errorf("partner_folder is required")
		}
		concurrency := in.Concurrency
		if concurrency <= 0 {
			concurrency = evidence.DefaultConcurrency
		}

		declared, err := loadDeclaredControlsMCP(in.PartnerFolder, in.AppType)
		if err != nil {
			return "", err
		}
		if len(declared) == 0 {
			return "", fmt.Errorf("no declared controls found in %s/partner_responses.csv", in.PartnerFolder)
		}

		v, err := factory(ctx, prompts.DefaultSystemMode, concurrency, nil)
		if err != nil {
			return "", err
		}

		cfg, err := awsauth.LoadConfig(ctx, os.Getenv("AWS_REGION"), os.Getenv("AWS_PROFILE"))
		if err != nil {
			return "", err
		}
		bedrockCli := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
			o.RetryMaxAttempts = validator.DefaultMaxAttempts
		})

		modelID := in.ModelID
		if modelID == "" {
			modelID = evidence.DefaultModelID
		}

		m, err := evidence.BuildEvidenceMap(ctx, in.PartnerFolder, evidence.Options{
			Bedrock:          bedrockCli,
			FileProcessor:    v,
			ModelID:          modelID,
			Concurrency:      concurrency,
			DeclaredControls: declared,
			ThorVersion:      version.Short(),
		})
		if err != nil {
			return "", err
		}
		path, err := evidence.SaveMap(in.PartnerFolder, m)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Wrote %s — %d/%d declared controls mapped, %d empty, %d files unmapped",
			path, len(m.Controls), len(m.DeclaredControls), len(m.EmptyControls), len(m.Unmapped)), nil
	}
}

// ensureEvidenceMapMCP mirrors cli.ensureEvidenceMap. Loads
// evidence_map.json if it matches the partner folder hash AND the
// declared-control set; otherwise rebuilds via Bedrock and persists.
func ensureEvidenceMapMCP(ctx context.Context, partnerFolder string, declaredControls []string,
	factory ValidatorFactory, mode prompts.SystemMode, concurrency int) (*evidence.Map, error) {

	if len(declaredControls) == 0 {
		return nil, fmt.Errorf("no declared controls (partner_responses.csv produced none)")
	}

	currentHash, err := runlog.HashPartnerFolder(partnerFolder)
	if err != nil {
		return nil, fmt.Errorf("hash partner folder: %w", err)
	}
	existing, err := evidence.LoadMap(partnerFolder)
	if err != nil {
		return nil, fmt.Errorf("load evidence map: %w", err)
	}
	if existing != nil && !evidence.IsStale(existing, currentHash) &&
		declaredControlsMatchMCP(existing.DeclaredControls, declaredControls) {
		return existing, nil
	}

	v, err := factory(ctx, mode, concurrency, nil)
	if err != nil {
		return nil, err
	}
	cfg, err := awsauth.LoadConfig(ctx, os.Getenv("AWS_REGION"), os.Getenv("AWS_PROFILE"))
	if err != nil {
		return nil, err
	}
	bedrockCli := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
		o.RetryMaxAttempts = validator.DefaultMaxAttempts
	})
	m, err := evidence.BuildEvidenceMap(ctx, partnerFolder, evidence.Options{
		Bedrock:          bedrockCli,
		FileProcessor:    v,
		Concurrency:      concurrency,
		DeclaredControls: declaredControls,
		ThorVersion:      version.Short(),
	})
	if err != nil {
		return nil, err
	}
	if _, err := evidence.SaveMap(partnerFolder, m); err != nil {
		return nil, fmt.Errorf("save evidence map: %w", err)
	}
	return m, nil
}

// loadDeclaredControlsMCP returns the suffix-mapped controlId set
// sourced from partner_responses.csv. Auto-converts from xlsx if the
// CSV is absent. Mirrors cli.loadDeclaredControls.
func loadDeclaredControlsMCP(folder, appTypeStr string) ([]string, error) {
	csvPath := filepath.Join(folder, "partner_responses.csv")
	if _, err := os.Stat(csvPath); err != nil {
		t, err := parseAppType(appTypeStr, excel.AppTypeSoftware)
		if err != nil {
			return nil, err
		}
		xlsx, err := findExcelFile(folder)
		if err != nil {
			return nil, err
		}
		if err := excel.ExtractResponses(xlsx, csvPath, t, nil); err != nil {
			return nil, fmt.Errorf("convert: %w", err)
		}
	}
	rows, err := excel.LoadResponses(csvPath)
	if err != nil {
		return nil, err
	}
	ctxRows, err := controls.LoadMap()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(rows))
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if _, dup := seen[r.ControlID]; dup {
			continue
		}
		if _, ok := ctxRows[r.ControlID]; !ok {
			continue
		}
		seen[r.ControlID] = struct{}{}
		out = append(out, r.ControlID)
	}
	return out, nil
}

func declaredControlsMatchMCP(persisted, current []string) bool {
	if len(persisted) != len(current) {
		return false
	}
	a := make(map[string]struct{}, len(persisted))
	for _, id := range persisted {
		a[id] = struct{}{}
	}
	for _, id := range current {
		if _, ok := a[id]; !ok {
			return false
		}
	}
	return true
}

func mapFilterMCP(m *evidence.Map) validator.EvidenceFilter {
	emptySet := make(map[string]struct{}, len(m.EmptyControls))
	for _, id := range m.EmptyControls {
		emptySet[id] = struct{}{}
	}
	return func(controlID string) ([]string, bool) {
		if _, isEmpty := emptySet[controlID]; isEmpty {
			return []string{}, true
		}
		files, ok := m.Controls[controlID]
		if !ok {
			return nil, false
		}
		return files, true
	}
}

func handleExport(_ context.Context, in exportInput) (string, error) {
	if in.PartnerFolder == "" {
		return "", fmt.Errorf("partner_folder is required")
	}
	mdPath, err := pickReportForExport(in.PartnerFolder, in.Report)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(mdPath) //nolint:gosec // path resolved from partner folder
	if err != nil {
		return "", err
	}
	htmlBody := report.RenderHTML(string(body), filepath.Base(in.PartnerFolder))
	out := filepath.Join(in.PartnerFolder, fmt.Sprintf("validation_report_%s.html", filepath.Base(in.PartnerFolder)))
	if err := os.WriteFile(out, []byte(htmlBody), 0o600); err != nil { //nolint:gosec // user-supplied folder + fixed filename
		return "", err
	}
	return fmt.Sprintf("Exported HTML to %s", out), nil
}

// ------------------------------------------------------------------
// Shared helpers.
// ------------------------------------------------------------------

func parseAppType(in string, fallback excel.AppType) (excel.AppType, error) {
	if in == "" {
		return fallback, nil
	}
	switch strings.ToUpper(in) {
	case "SOFTWARE":
		return excel.AppTypeSoftware, nil
	case "SERVICE":
		return excel.AppTypeService, nil
	default:
		return "", fmt.Errorf("invalid app_type %q (want SERVICE or SOFTWARE)", in)
	}
}

func findExcelFile(folder string) (string, error) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", folder, err)
	}
	var best string
	var bestMod int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "~$") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".xlsx" && ext != ".xls" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		mod := info.ModTime().UnixNano()
		if mod >= bestMod {
			best = filepath.Join(folder, name)
			bestMod = mod
		}
	}
	if best == "" {
		return "", fmt.Errorf("no Excel file found in %s", folder)
	}
	return best, nil
}

func uniqueResponseIDs(rows []excel.Response) []string {
	seen := make(map[string]struct{}, len(rows))
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if _, ok := seen[r.ControlID]; ok {
			continue
		}
		seen[r.ControlID] = struct{}{}
		out = append(out, r.ControlID)
	}
	return out
}

func countByStatus(results map[string]validator.Result) (passed, failed, waived, errored int) {
	for _, r := range results {
		switch r.Status {
		case validator.StatusPassed:
			passed++
		case validator.StatusFailed:
			failed++
		case validator.StatusWaived:
			waived++
		case validator.StatusErrored:
			errored++
		}
	}
	return
}

func writeReports(folder string, order []string, results map[string]validator.Result,
	mode prompts.SystemMode, startedAt, completedAt time.Time,
) error {
	ordered := make([]validator.Result, 0, len(order))
	for _, id := range order {
		if r, ok := results[id]; ok {
			ordered = append(ordered, r)
		}
	}
	body := report.RenderMarkdown(report.Summary{
		PartnerName: filepath.Base(folder),
		Timestamp:   startedAt,
		Results:     ordered,
	})

	rootPath := filepath.Join(folder, "validation_summary.md")
	if err := os.WriteFile(rootPath, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", rootPath, err)
	}
	summaryDir := filepath.Join(folder, "reports", "summary")
	if err := os.MkdirAll(summaryDir, 0o750); err != nil {
		return err
	}
	stamp := startedAt.Format("20060102_150405")
	timestamped := filepath.Join(summaryDir, "validation_summary_"+stamp+".md")
	if err := os.WriteFile(timestamped, []byte(body), 0o600); err != nil {
		return err
	}

	hash, err := runlog.HashPartnerFolder(folder)
	if err != nil {
		return err
	}
	rec := make([]runlog.ResultRecord, 0, len(order))
	for _, id := range order {
		r, ok := results[id]
		if !ok {
			continue
		}
		rr := runlog.ResultRecord{
			ControlID: r.ControlID,
			Status:    r.Status.String(),
			Verdict:   r.Status.Verdict(),
			Reasoning: r.Reasoning,
		}
		if r.Err != nil {
			rr.Error = r.Err.Error()
		}
		rec = append(rec, rr)
	}
	_, err = runlog.WriteManifest(folder, runlog.RunManifest{
		ID:                runlog.NewRunID(),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		ThorVersion:       version.Short(),
		ModelID:           validator.DefaultModelID,
		PromptVersion:     string(mode),
		PartnerFolderHash: hash,
		Controls:          order,
		Results:           rec,
	})
	return err
}

func pickReportForExport(folder, name string) (string, error) {
	if name != "" {
		reports, err := report.ListReports(folder)
		if err != nil {
			return "", err
		}
		if r := report.FindReport(reports, name); r != nil {
			return r.Path, nil
		}
		return "", fmt.Errorf("no report matching %q", name)
	}
	root := filepath.Join(folder, "validation_summary.md")
	if _, err := os.Stat(root); err == nil {
		return root, nil
	}
	reports, err := report.ListReports(folder)
	if err != nil {
		return "", err
	}
	if len(reports) == 0 {
		return "", fmt.Errorf("no validation_summary.md found in %s", folder)
	}
	return reports[len(reports)-1].Path, nil
}
