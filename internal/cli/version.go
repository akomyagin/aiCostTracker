package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// versionCmd prints build metadata injected via ldflags.
func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "aicost %s (commit %s, built %s)\n",
				a.Version, a.Commit, a.Date)
			return err
		},
	}
}
