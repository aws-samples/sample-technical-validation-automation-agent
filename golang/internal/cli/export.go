package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"thor-golang/internal/report"
)

func newExportCommand() *cobra.Command {
	var reportName string
	cmd := &cobra.Command{
		Use:   "export <partner-folder>",
		Short: "Render a validation_summary.md to a self-contained HTML file next to it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			folder := args[0]
			mdPath, err := pickReport(folder, reportName)
			if err != nil {
				return err
			}
			body, err := os.ReadFile(mdPath) //nolint:gosec // path resolved above
			if err != nil {
				return fmt.Errorf("read %s: %w", mdPath, err)
			}
			html := report.RenderHTML(string(body), filepath.Base(folder))
			outPath := filepath.Join(folder, fmt.Sprintf("validation_report_%s.html", filepath.Base(folder)))
			if err := os.WriteFile(outPath, []byte(html), 0o600); err != nil { //nolint:gosec // outPath is built from user-supplied folder + a fixed filename
				return fmt.Errorf("write %s: %w", outPath, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "thor export: %s\n", outPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&reportName, "report", "",
		"timestamp substring of a specific report under reports/summary/ (default: root validation_summary.md or latest)")
	return cmd
}

// pickReport selects the markdown to render. Priority:
//  1. --report substring matched against reports/summary/*.md
//  2. <folder>/validation_summary.md (root copy)
//  3. latest reports/summary/validation_summary_<ts>.md
func pickReport(folder, name string) (string, error) {
	if name != "" {
		reports, err := report.ListReports(folder)
		if err != nil {
			return "", err
		}
		if r := report.FindReport(reports, name); r != nil {
			return r.Path, nil
		}
		return "", &errExitCode{code: ExitUsageError,
			err: fmt.Errorf("no report matching %q under %s/reports/summary", name, folder)}
	}
	root := filepath.Join(folder, "validation_summary.md")
	if _, err := os.Stat(root); err == nil {
		return root, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
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
