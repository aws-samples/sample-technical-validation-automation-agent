package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"thor-golang/internal/controls"
)

func newListControlsCommand() *cobra.Command {
	var category string
	cmd := &cobra.Command{
		Use:   "list-controls",
		Short: "Print the controls Thor knows about (optionally filtered by --category)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rows, err := controls.Load()
			if err != nil {
				return err
			}
			ids := make([]string, 0, len(rows))
			for _, r := range rows {
				ids = append(ids, r.ID)
			}
			if category != "" {
				ids = controls.GetApplicable(category, ids)
			}

			out := cmd.OutOrStdout()
			if category != "" {
				fmt.Fprintf(out, "# Controls for: %s (%d)\n", category, len(ids))
			} else {
				fmt.Fprintf(out, "# All Controls (%d)\n", len(ids))
			}
			for _, id := range ids {
				fmt.Fprintf(out, "- %s\n", id)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "filter by designation category")
	return cmd
}
