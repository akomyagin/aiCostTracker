package cli

import (
	"fmt"

	"github.com/akomyagin/aiCostTracker/internal/report"

	"github.com/spf13/cobra"
)

// historyCmd reads stored snapshots from the local SQLite history and prints an
// aggregated table for the period — no network calls under any flag.
func (a *App) historyCmd() *cobra.Command {
	var (
		period      string
		chart       bool
		byModel     bool
		compare     bool
		failOnAlert bool
	)

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show stored usage from local history (no network)",
		Long: "Read previously stored snapshots from the local SQLite history and print\n" +
			"an aggregated table for the given period. Does not call any provider API.\n" +
			"\n" +
			"Use --chart for an ASCII bar chart of daily spend, --by-model to break the\n" +
			"table down by (provider, model), or --compare to show this period next to\n" +
			"the previous one of the same kind. --compare cannot be combined with the\n" +
			"other two.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runHistory(cmd, period, chart, byModel, compare, failOnAlert)
		},
	}
	cmd.Flags().StringVar(&period, "period", "30d", "period to show: Nd (e.g. 7d), month, or today")
	cmd.Flags().BoolVar(&chart, "chart", false, "append an ASCII bar chart of daily spend")
	cmd.Flags().BoolVar(&byModel, "by-model", false, "break the table down by (provider, model)")
	cmd.Flags().BoolVar(&compare, "compare", false, "compare with the previous period of the same kind")
	cmd.Flags().BoolVar(&failOnAlert, "fail-on-alert", false, "exit non-zero when spend exceeds alert.monthly_usd")
	return cmd
}

func (a *App) runHistory(cmd *cobra.Command, period string, chart, byModel, compare, failOnAlert bool) error {
	ctx := cmd.Context()

	if compare && (byModel || chart) {
		return fmt.Errorf("--compare cannot be combined with --by-model or --chart")
	}

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

	out := cmd.OutOrStdout()

	if len(report.Aggregate(records)) == 0 {
		// history never touches the network, so it cannot distinguish "zero
		// spend" from "not fetched yet" — say so plainly instead of asserting.
		fmt.Fprintln(out, "No stored history for this period. Run \"aicost report\" for this period to see whether it's zero spend or just not fetched yet.")
		return nil
	}

	// Alert is computed from the CURRENT window's total regardless of output
	// format; under --compare the previous period is irrelevant to the threshold.
	cur := report.Aggregate(records)
	total := sumCost(cur)

	if compare {
		prevWin, err := previousWindow(period, a.Now())
		if err != nil {
			return err
		}
		prevRecords, err := store.Query(ctx, "", prevWin)
		if err != nil {
			return fmt.Errorf("query history: %w", err)
		}
		if err := report.CompareTables(out, formatWindow(window), formatWindow(prevWin),
			cur, report.Aggregate(prevRecords)); err != nil {
			return fmt.Errorf("render comparison: %w", err)
		}
		return checkAlert(cmd, cfg.Alert.MonthlyUSD, total, failOnAlert)
	}

	if byModel {
		if err := report.ModelTable(out, report.AggregateByModel(records)); err != nil {
			return fmt.Errorf("render table: %w", err)
		}
	} else {
		if err := report.Table(out, cur); err != nil {
			return fmt.Errorf("render table: %w", err)
		}
	}

	if chart {
		fmt.Fprintln(out)
		if err := report.BarChart(out, report.AggregateByDay(records, window)); err != nil {
			return fmt.Errorf("render chart: %w", err)
		}
	}
	return checkAlert(cmd, cfg.Alert.MonthlyUSD, total, failOnAlert)
}
