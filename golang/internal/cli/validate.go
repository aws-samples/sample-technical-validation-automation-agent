package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/spf13/cobra"

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

// validateOptions holds the parsed --controls/--category/etc. for one
// invocation. Pulled out so tests can drive runValidate directly.
type validateOptions struct {
	controlsArg    string
	category       string
	consensus      int
	concurrency    int
	skipConversion bool
	noMap          bool
	systemMode     string
	appType        string
}

// validateDeps lets tests inject a fake Validator constructor so we
// don't talk to live Bedrock. Production wires this to awsauth.LoadConfig
// + validator.NewValidator.
type validateDeps struct {
	newValidator func(ctx context.Context, mode prompts.SystemMode, concurrency int,
		filter validator.EvidenceFilter) (*validator.Validator, error)
	// buildMap mirrors evidence.BuildEvidenceMap so cli_test.go can
	// inject a stub map without going through real Bedrock. The
	// declaredControls list is the suffix-mapped controlId set sourced
	// from partner_responses.csv — the mapper restricts assignments to
	// these controls.
	buildMap func(ctx context.Context, partnerFolder string, declaredControls []string,
		newFileProcessor func() (evidence.FileProcessor, error)) (*evidence.Map, error)
}

func newValidateCommand() *cobra.Command {
	opts := validateOptions{
		consensus:   1,
		concurrency: validator.DefaultConcurrency,
		systemMode:  string(prompts.DefaultSystemMode),
		appType:     "SOFTWARE",
	}
	cmd := &cobra.Command{
		Use:   "validate <partner-folder>",
		Short: "Validate a partner folder against AWS PSA controls",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, args[0], opts, resolveValidateDeps())
		},
	}
	cmd.Flags().StringVar(&opts.controlsArg, "controls", "",
		"space-separated control IDs (e.g. \"ACCT-001 COST-001\")")
	cmd.Flags().StringVar(&opts.category, "category", "",
		"designation category (filters DC controls)")
	cmd.Flags().IntVar(&opts.consensus, "consensus", 1,
		"number of independent runs to majority-vote (1 = no consensus)")
	cmd.Flags().IntVar(&opts.concurrency, "concurrency", validator.DefaultConcurrency,
		"max in-flight Bedrock calls")
	cmd.Flags().BoolVar(&opts.skipConversion, "skip-conversion", false,
		"reuse an existing partner_responses.csv instead of re-extracting from Excel")
	cmd.Flags().BoolVar(&opts.noMap, "no-map", false,
		"skip the evidence-map pre-pass and send every supporting_doc to every control")
	cmd.Flags().StringVar(&opts.systemMode, "system-mode", string(prompts.DefaultSystemMode),
		"system prompt to use (old | new | revised)")
	cmd.Flags().StringVar(&opts.appType, "app-type", "SOFTWARE",
		"application type used during conversion (SERVICE | SOFTWARE)")
	return cmd
}

// resolveValidateDeps returns the production validateDeps unless tests
// have overridden it via setValidateDepsForTesting (defined in
// cli_test.go to keep the seam test-only).
func resolveValidateDeps() validateDeps {
	if testValidateDepsOverride != nil {
		return *testValidateDepsOverride
	}
	return defaultValidateDeps()
}

// testValidateDepsOverride is set by tests (cli_test.go) and read by
// resolveValidateDeps. Production code never assigns to it.
var testValidateDepsOverride *validateDeps

func defaultValidateDeps() validateDeps {
	return validateDeps{
		newValidator: func(ctx context.Context, mode prompts.SystemMode, concurrency int,
			filter validator.EvidenceFilter) (*validator.Validator, error) {
			cfg, err := awsauth.LoadConfig(ctx, global.Region, global.Profile)
			if err != nil {
				return nil, err
			}
			return validator.NewValidator(validator.Options{
				AWSConfig:      cfg,
				SystemMode:     mode,
				Concurrency:    concurrency,
				EvidenceFilter: filter,
			})
		},
		buildMap: func(ctx context.Context, partnerFolder string, declaredControls []string,
			newFileProcessor func() (evidence.FileProcessor, error)) (*evidence.Map, error) {
			cfg, err := awsauth.LoadConfig(ctx, global.Region, global.Profile)
			if err != nil {
				return nil, err
			}
			fp, err := newFileProcessor()
			if err != nil {
				return nil, err
			}
			bedrockCli := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
				o.RetryMaxAttempts = validator.DefaultMaxAttempts
			})
			return evidence.BuildEvidenceMap(ctx, partnerFolder, evidence.Options{
				Bedrock:          bedrockCli,
				FileProcessor:    fp,
				DeclaredControls: declaredControls,
				ThorVersion:      version.Short(),
			})
		},
	}
}

func runValidate(cmd *cobra.Command, folder string, opts validateOptions, deps validateDeps) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	mode := prompts.SystemMode(strings.ToLower(opts.systemMode))
	if !mode.Valid() {
		return &errExitCode{code: ExitUsageError,
			err: fmt.Errorf("invalid --system-mode %q (want old, new, or revised)", opts.systemMode)}
	}

	// Run conversion unless explicitly skipped.
	if !opts.skipConversion {
		appType, err := parseAppType(opts.appType)
		if err != nil {
			return &errExitCode{code: ExitUsageError, err: err}
		}
		xlsx, ferr := findExcelFile(folder)
		if ferr == nil {
			outPath := filepath.Join(folder, "partner_responses.csv")
			if err := excel.ExtractResponses(xlsx, outPath, appType, nil); err != nil {
				return fmt.Errorf("convert: %w", err)
			}
		} else {
			// No Excel found: only acceptable if the partner CSV is already there.
			csvPath := filepath.Join(folder, "partner_responses.csv")
			if _, err := os.Stat(csvPath); err != nil {
				return ferr
			}
		}
	}

	// Decide which controls to run.
	csvPath := filepath.Join(folder, "partner_responses.csv")
	rows, err := excel.LoadResponses(csvPath)
	if err != nil {
		return err
	}
	allFromCSV := uniqueIDs(rows)

	// Filter to control IDs the validator actually knows how to handle.
	// Mirrors the Python `valid_control_ids` filter so misconfigured CSVs
	// don't make us iterate phantom controls.
	contextRows, err := controls.LoadMap()
	if err != nil {
		return err
	}
	known := make(map[string]struct{}, len(contextRows))
	for id := range contextRows {
		known[id] = struct{}{}
	}
	// DOC-006 lives in CONTEXT.csv but routes to the marketplace special
	// check (no Bedrock); keep it.

	filtered := make([]string, 0, len(allFromCSV))
	for _, id := range allFromCSV {
		if _, ok := known[id]; ok {
			filtered = append(filtered, id)
		}
	}

	var controlIDs []string
	switch {
	case opts.controlsArg != "":
		controlIDs = strings.Fields(opts.controlsArg)
	case opts.category != "":
		controlIDs = controls.GetApplicable(opts.category, filtered)
	default:
		controlIDs = filtered
	}
	if len(controlIDs) == 0 {
		return &errExitCode{code: ExitUsageError,
			err: errors.New("no controls to validate; check --controls / --category / partner_responses.csv")}
	}

	// B17b: surface controls referenced in CONTEXT.csv that the partner
	// CSV failed to produce after suffix mapping.
	contextIDs := make([]string, 0, len(contextRows))
	for id := range contextRows {
		contextIDs = append(contextIDs, id)
	}
	for _, w := range controls.SuffixDriftWarnings(contextIDs, allFromCSV) {
		fmt.Fprintf(cmd.ErrOrStderr(), "WARN: %s\n", w)
	}

	// Evidence map: default behaviour is to ensure a fresh map exists
	// before validation. The mapper is driven by `filtered` — the
	// declared-control set from partner_responses.csv. --no-map opts
	// out and falls back to today's "send every file to every control"
	// behaviour (still safe, just pays for the page-budget demotion
	// path on big folders).
	var filter validator.EvidenceFilter
	if !opts.noMap {
		m, err := ensureEvidenceMap(ctx, cmd, folder, filtered, opts.concurrency, deps)
		if err != nil {
			if errors.Is(err, awsauth.ErrExpiredCredentials) {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(),
				"WARN: evidence map unavailable (%v); falling back to all-files-per-control\n", err)
		} else if m != nil {
			filter = mapFilter(m)
			for _, name := range m.Unmapped {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"WARN: %s mapped to no controls — review manually or rerun `thor map`\n", name)
			}
			for _, id := range m.EmptyControls {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"WARN: control %s is declared but has no evidence files attached — partner-response only\n", id)
			}
		}
	}

	// Build the validator. Tests inject a stub via deps.
	v, err := deps.newValidator(ctx, mode, opts.concurrency, filter)
	if err != nil {
		return err
	}

	startedAt := time.Now().UTC()
	results, err := v.ValidateBatch(ctx, controlIDs, folder, validator.BatchOptions{
		Concurrency:   opts.concurrency,
		ConsensusRuns: opts.consensus,
		Progress: func(e validator.ProgressEvent) {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"  [%d/%d] %s -> %s\n", e.Done, e.Total, e.ControlID, e.Result.Status)
		},
	})
	if err != nil {
		if errors.Is(err, awsauth.ErrExpiredCredentials) {
			return err
		}
		return fmt.Errorf("validate: %w", err)
	}
	completedAt := time.Now().UTC()

	// Write reports/summary/validation_summary_<ts>.md + root copy.
	if err := writeSummary(folder, results, controlIDs, startedAt); err != nil {
		return err
	}

	// Write the auditable run manifest.
	if err := writeManifest(folder, results, controlIDs, mode, startedAt, completedAt); err != nil {
		// Manifest failure shouldn't fail the validation overall; warn loudly.
		fmt.Fprintf(cmd.ErrOrStderr(), "WARN: manifest: %v\n", err)
	}

	// Summary line + exit-code decision.
	passed, failed, waived, errored := countByStatus(results)
	fmt.Fprintf(cmd.OutOrStdout(),
		"thor validate: %d controls — passed=%d failed=%d waived=%d errored=%d\n",
		len(controlIDs), passed, failed, waived, errored)
	if errored > 0 {
		return &errExitCode{code: ExitPartialFailure,
			err: fmt.Errorf("%d control(s) errored — see report and validation_progress.log", errored)}
	}
	return nil
}

// ensureEvidenceMap loads evidence_map.json if it exists and matches the
// current partner folder hash AND covers the declared-control set;
// otherwise it builds a fresh map and persists it. Returns the
// loaded/built map, or (nil, err) on failure.
func ensureEvidenceMap(ctx context.Context, cmd *cobra.Command, folder string,
	declaredControls []string, concurrency int, deps validateDeps) (*evidence.Map, error) {

	if len(declaredControls) == 0 {
		return nil, fmt.Errorf("no declared controls (partner_responses.csv produced none)")
	}

	currentHash, err := runlog.HashPartnerFolder(folder)
	if err != nil {
		return nil, fmt.Errorf("hash partner folder: %w", err)
	}

	existing, err := evidence.LoadMap(folder)
	if err != nil {
		return nil, fmt.Errorf("load evidence map: %w", err)
	}
	if existing != nil && !evidence.IsStale(existing, currentHash) &&
		declaredControlsMatch(existing.DeclaredControls, declaredControls) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"thor validate: evidence map up-to-date (%d controls mapped, %d empty)\n",
			len(existing.Controls), len(existing.EmptyControls))
		return existing, nil
	}
	switch {
	case existing == nil:
		fmt.Fprintf(cmd.ErrOrStderr(),
			"thor validate: building evidence map (one-time pre-pass per folder)\n")
	case evidence.IsStale(existing, currentHash):
		fmt.Fprintf(cmd.ErrOrStderr(),
			"thor validate: evidence map stale (folder hash or schema changed) — rebuilding\n")
	default:
		fmt.Fprintf(cmd.ErrOrStderr(),
			"thor validate: declared-control set changed — rebuilding evidence map\n")
	}

	m, err := deps.buildMap(ctx, folder, declaredControls, func() (evidence.FileProcessor, error) {
		// We need a Validator instance just for ProcessFileForEvidence.
		// SystemMode/Concurrency don't matter here — only the file
		// routing logic does.
		v, err := deps.newValidator(ctx, prompts.DefaultSystemMode, concurrency, nil)
		if err != nil {
			return nil, err
		}
		return v, nil
	})
	if err != nil {
		return nil, err
	}
	if _, err := evidence.SaveMap(folder, m); err != nil {
		return nil, fmt.Errorf("save evidence map: %w", err)
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"thor validate: evidence map built — %d controls mapped, %d empty, %d files unmapped\n",
		len(m.Controls), len(m.EmptyControls), len(m.Unmapped))
	return m, nil
}

// declaredControlsMatch reports whether the persisted map's
// DeclaredControls list matches the current declared set (order
// independent). Mismatch invalidates the cache.
func declaredControlsMatch(persisted, current []string) bool {
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

// mapFilter adapts an evidence.Map into a validator.EvidenceFilter.
//
// Behaviour:
//   - Declared control with mapped files → returns (files, true) in
//     mapper rank order.
//   - Declared control listed in EmptyControls → returns ([], true)
//     so the validator sends partner-response only (no evidence). This
//     forces a clean FAIL signal when the partner declared a control
//     they have no documentation for.
//   - Anything else (undeclared control, control the mapper never saw)
//     → returns (nil, false), letting the validator fall through to
//     today's unfiltered "send everything" behaviour.
func mapFilter(m *evidence.Map) validator.EvidenceFilter {
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

func uniqueIDs(rows []excel.Response) []string {
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

// writeSummary persists the markdown report to the canonical paths
// (root validation_summary.md + reports/summary/validation_summary_<ts>.md).
func writeSummary(folder string, results map[string]validator.Result,
	order []string, startedAt time.Time) error {

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
		return fmt.Errorf("mkdir %s: %w", summaryDir, err)
	}
	stamp := startedAt.Format("20060102_150405")
	timestampedPath := filepath.Join(summaryDir, "validation_summary_"+stamp+".md")
	if err := os.WriteFile(timestampedPath, []byte(body), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", timestampedPath, err)
	}
	return nil
}

// writeManifest persists the runlog.RunManifest auditable artifact.
func writeManifest(folder string, results map[string]validator.Result,
	order []string, mode prompts.SystemMode, startedAt, completedAt time.Time) error {

	hash, err := runlog.HashPartnerFolder(folder)
	if err != nil {
		return err
	}
	rec := make([]runlog.ResultRecord, 0, len(order))
	sortedOrder := append([]string(nil), order...)
	sort.Strings(sortedOrder)
	for _, id := range sortedOrder {
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
		Controls:          sortedOrder,
		Results:           rec,
	})
	return err
}
