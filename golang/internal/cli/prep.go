package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"thor-golang/internal/controls"
)

func newPrepCommand() *cobra.Command {
	var (
		category string
		appType  string
		folder   string
	)
	cmd := &cobra.Command{
		Use:   "prep <partner-name>",
		Short: "Create a partner folder skeleton ready for validation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			partnerName := args[0]
			if category == "" {
				return &errExitCode{code: ExitUsageError, err: fmt.Errorf("--category is required")}
			}
			t, err := parseAppType(appType)
			if err != nil {
				return &errExitCode{code: ExitUsageError, err: err}
			}

			base := folder
			if base == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				base = filepath.Join(home, "Documents", "PSA_Validations")
			}
			partnerFolder := filepath.Join(base, partnerName)
			supporting := filepath.Join(partnerFolder, "supporting_docs")
			summary := filepath.Join(partnerFolder, "reports", "summary")

			for _, p := range []string{partnerFolder, supporting, summary} {
				if err := os.MkdirAll(p, 0o750); err != nil {
					return fmt.Errorf("mkdir %s: %w", p, err)
				}
			}

			rows, err := controls.Load()
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(rows))
			for _, r := range rows {
				ids = append(ids, r.ID)
			}
			applicable := controls.GetApplicable(category, ids)

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "thor prep: created %s\n\n", partnerFolder)
			fmt.Fprintf(out, "  category:           %s\n", category)
			fmt.Fprintf(out, "  app type:           %s\n", t)
			fmt.Fprintf(out, "  applicable controls: %d\n\n", len(applicable))
			fmt.Fprintln(out, "Next:")
			fmt.Fprintln(out, "  1. Drop the partner Excel checklist into the partner folder")
			fmt.Fprintln(out, "  2. Drop supporting evidence into supporting_docs/")
			fmt.Fprintln(out, "  3. Run `thor convert` then `thor validate`")
			return nil
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "designation category (required)")
	cmd.Flags().StringVar(&appType, "app-type", "SOFTWARE", "application type (SERVICE | SOFTWARE)")
	cmd.Flags().StringVar(&folder, "folder", "",
		"base folder under which to create the partner directory (default ~/Documents/PSA_Validations)")
	return cmd
}
