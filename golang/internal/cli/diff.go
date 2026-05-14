package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"thor-golang/internal/report"
)

func newDiffCommand() *cobra.Command {
	var (
		mode string
		run1 string
		run2 string
	)
	cmd := &cobra.Command{
		Use:   "diff <partner-folder>",
		Short: "Compare two validation runs (or list a timeline across all runs)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			folder := args[0]
			reports, err := report.ListReports(folder)
			if err != nil {
				return err
			}
			if len(reports) < 2 && mode != "all" {
				return fmt.Errorf("need at least 2 validation reports to diff; found %d in %s",
					len(reports), filepath.Join(folder, "reports", "summary"))
			}

			switch mode {
			case "", "latest":
				return emitDiff(cmd, folder, reports[len(reports)-2], reports[len(reports)-1])
			case "all":
				body, err := report.RenderTimeline(filepath.Base(folder), reports)
				if err != nil {
					return err
				}
				fmt.Fprint(cmd.OutOrStdout(), body)
				return nil
			case "custom":
				r1 := report.FindReport(reports, run1)
				if r1 == nil {
					if run1 == "" {
						r1 = &reports[0]
					} else {
						return &errExitCode{code: ExitUsageError,
							err: fmt.Errorf("no report matching %q", run1)}
					}
				}
				r2 := report.FindReport(reports, run2)
				if r2 == nil {
					if run2 == "" {
						r2 = &reports[len(reports)-1]
					} else {
						return &errExitCode{code: ExitUsageError,
							err: fmt.Errorf("no report matching %q", run2)}
					}
				}
				return emitDiff(cmd, folder, *r1, *r2)
			default:
				return &errExitCode{code: ExitUsageError,
					err: fmt.Errorf("invalid --mode %q (want latest, all, or custom)", mode)}
			}
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "latest",
		"diff mode (latest | all | custom)")
	cmd.Flags().StringVar(&run1, "run1", "",
		"first run for --mode custom (timestamp substring)")
	cmd.Flags().StringVar(&run2, "run2", "",
		"second run for --mode custom (timestamp substring)")
	return cmd
}

func emitDiff(cmd *cobra.Command, folder string, r1, r2 report.ReportFile) error {
	res1, err := report.ParseSummary(r1.Path)
	if err != nil {
		return err
	}
	res2, err := report.ParseSummary(r2.Path)
	if err != nil {
		return err
	}
	body := report.RenderDiff(filepath.Base(folder), r1.Timestamp, r2.Timestamp, res1, res2)
	fmt.Fprint(cmd.OutOrStdout(), body)
	return nil
}
