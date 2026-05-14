package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"thor-golang/internal/excel"
)

func newConvertCommand() *cobra.Command {
	var (
		appType string
	)
	cmd := &cobra.Command{
		Use:   "convert <partner-folder>",
		Short: "Extract partner_responses.csv from the Excel checklist in a partner folder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			folder := args[0]
			t, err := parseAppType(appType)
			if err != nil {
				return &errExitCode{code: ExitUsageError, err: err}
			}
			xlsx, err := findExcelFile(folder)
			if err != nil {
				return err
			}
			outPath := filepath.Join(folder, "partner_responses.csv")
			if err := excel.ExtractResponses(xlsx, outPath, t, nil); err != nil {
				return err
			}
			rows, err := excel.LoadResponses(outPath)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"thor convert: extracted %d response(s) from %s -> %s\n",
				len(rows), xlsx, outPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&appType, "app-type", "SOFTWARE",
		"application type for suffix mapping (SERVICE | SOFTWARE)")
	return cmd
}

// parseAppType validates the user-supplied --app-type flag and returns
// the corresponding excel.AppType.
func parseAppType(in string) (excel.AppType, error) {
	switch strings.ToUpper(in) {
	case "SOFTWARE":
		return excel.AppTypeSoftware, nil
	case "SERVICE":
		return excel.AppTypeService, nil
	default:
		return "", fmt.Errorf("invalid --app-type %q (want SERVICE or SOFTWARE)", in)
	}
}

// findExcelFile picks the most recently-modified .xlsx in folder, skipping
// Office tilde locks. Mirrors the Python `_find_excel_file` heuristic.
func findExcelFile(folder string) (string, error) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", &errExitCode{code: ExitUsageError,
				err: fmt.Errorf("partner folder does not exist: %s", folder)}
		}
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
