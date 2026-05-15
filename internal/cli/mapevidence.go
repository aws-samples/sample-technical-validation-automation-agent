package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/spf13/cobra"

	"thor-golang/internal/awsauth"
	"thor-golang/internal/controls"
	"thor-golang/internal/evidence"
	"thor-golang/internal/excel"
	"thor-golang/internal/validator"
	"thor-golang/internal/version"
)

// newMapCommand is `thor map`: builds the control->files evidence map
// for a partner folder, persisting it to evidence_map.json. Used both
// as a standalone pre-pass (when an operator wants to inspect/edit the
// map before validation) and implicitly by `thor validate` when no map
// exists or the existing one is stale.
//
// The mapper is now declared-driven: it requires partner_responses.csv
// (or the source xlsx, which it auto-converts) and only assigns files
// to controls the partner declared.
func newMapCommand() *cobra.Command {
	var (
		concurrency int
		modelID     string
		appType     string
	)
	cmd := &cobra.Command{
		Use:   "map <partner-folder>",
		Short: "Build the control→files evidence map (writes evidence_map.json)",
		Long: `Loads partner_responses.csv (extracting it from the partner's xlsx
checklist if absent), then sends each supporting_docs file to Bedrock
with a catalog of ONLY the declared controls and asks which controls
the file is relevant to. The aggregated control→files mapping is
persisted as evidence_map.json at the partner-folder root, with each
control's file list rank-ordered by mapper confidence (most relevant
first). ` + "`thor validate`" + ` consumes the map by default so each
control sees only the relevant subset of supporting_docs (capped at
Bedrock's 5-document-block limit).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			folder := args[0]
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			declared, err := loadDeclaredControls(folder, appType)
			if err != nil {
				return err
			}
			if len(declared) == 0 {
				return &errExitCode{code: ExitUsageError,
					err: fmt.Errorf("no declared controls found in %s/partner_responses.csv", folder)}
			}

			cfg, err := awsauth.LoadConfig(ctx, global.Region, global.Profile)
			if err != nil {
				return err
			}
			bedrockCli := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
				o.RetryMaxAttempts = validator.DefaultMaxAttempts
			})
			v, err := validator.NewValidator(validator.Options{
				AWSConfig: cfg,
				Bedrock:   bedrockCli,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.ErrOrStderr(),
				"thor map: building evidence map for %s — %d declared controls (model=%s)\n",
				folder, len(declared), modelID)

			m, err := evidence.BuildEvidenceMap(ctx, folder, evidence.Options{
				Bedrock:          bedrockCli,
				FileProcessor:    v,
				ModelID:          modelID,
				Concurrency:      concurrency,
				DeclaredControls: declared,
				ThorVersion:      version.Short(),
			})
			if err != nil {
				return fmt.Errorf("map: %w", err)
			}

			path, err := evidence.SaveMap(folder, m)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(folder, path)
			if rel == "" {
				rel = path
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"thor map: wrote %s — %d/%d declared controls have evidence, %d empty, %d files unmapped\n",
				rel, len(m.Controls), len(m.DeclaredControls), len(m.EmptyControls), len(m.Unmapped))
			for _, name := range m.Unmapped {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"  WARN: %s mapped to no controls — review manually\n", name)
			}
			for _, id := range m.EmptyControls {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"  WARN: control %s has no evidence — partner-response only at validate time\n", id)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&concurrency, "concurrency", evidence.DefaultConcurrency,
		"max in-flight Bedrock calls during mapping")
	cmd.Flags().StringVar(&modelID, "model-id", evidence.DefaultModelID,
		"Bedrock model ID for the mapping pre-pass")
	cmd.Flags().StringVar(&appType, "app-type", "SOFTWARE",
		"application type used during conversion (SERVICE | SOFTWARE)")
	return cmd
}

// loadDeclaredControls returns the suffix-mapped controlId set sourced
// from partner_responses.csv. If the CSV is absent, it runs the
// extraction pass on the partner's xlsx (matching `thor convert`).
// Filters out IDs that aren't in CONTEXT.csv so the mapper never sees
// drift it can't act on.
func loadDeclaredControls(folder, appTypeStr string) ([]string, error) {
	csvPath := filepath.Join(folder, "partner_responses.csv")
	if _, err := os.Stat(csvPath); err != nil {
		// Try to convert from xlsx.
		t, err := parseAppType(appTypeStr)
		if err != nil {
			return nil, &errExitCode{code: ExitUsageError, err: err}
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

	contextRows, err := controls.LoadMap()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(rows))
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if _, dup := seen[r.ControlID]; dup {
			continue
		}
		if _, ok := contextRows[r.ControlID]; !ok {
			continue
		}
		seen[r.ControlID] = struct{}{}
		out = append(out, r.ControlID)
	}
	return out, nil
}
