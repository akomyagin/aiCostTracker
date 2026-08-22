package cli

import (
	"fmt"

	"github.com/akomyagin/aiCostTracker/internal/report"

	"github.com/spf13/cobra"
)

// historyCmd reads stored snapshots from the local SQLite history and prints an
// aggregated table for the period — no network calls. Trends/charts are Фаза 2.
func (a *App) historyCmd() *cobra.Command {
	var period string

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show stored usage from local history (no network)",
		Long: "Read previously stored snapshots from the local SQLite history and print\n" +
			"an aggregated table for the given period. Does not call any provider API.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runHistory(cmd, period)
		},
	}
	cmd.Flags().StringVar(&period, "period", "30d", "period to show: Nd (e.g. 7d), month, or today")
	return cmd
}

func (a *App) runHistory(cmd *cobra.Command, period string) error {
	ctx := cmd.Context()

	cfg, err := a.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	window, err := parsePeriod(period, a.Now())
	if err != nil {
		return err
	}

	dbPath, err := cfg.ResolvedDBPath()
	if err != nil {
		return fmt.Errorf("resolve db path: %w", err)
	}
	store, err := a.OpenStore(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	records, err := store.Query(ctx, "", window)
	if err != nil {
		return fmt.Errorf("query history: %w", err)
	}

	rows := report.Aggregate(records)
	if len(rows) == 0 {
		// history never touches the network, so it cannot distinguish "zero
		// spend" from "not fetched yet" — say so plainly instead of asserting.
		fmt.Fprintln(cmd.OutOrStdout(), "No stored history for this period. Run \"aicost report\" for this period to see whether it's zero spend or just not fetched yet.")
		return nil
	}
	if err := report.Table(cmd.OutOrStdout(), rows); err != nil {
		return fmt.Errorf("render table: %w", err)
	}
	return nil
}
