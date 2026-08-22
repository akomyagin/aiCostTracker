package cli

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/akomyagin/aiCostTracker/internal/provider"
	"github.com/akomyagin/aiCostTracker/internal/report"

	"github.com/spf13/cobra"
)

// adminKeyHintFor returns a leading "\n" + adminKeyHint when err looks like an
// admin-key problem (missing key, or a 401/403 from the provider), so the user is
// told they likely used the wrong KIND of key. Returns "" otherwise, so ordinary
// network/parse errors don't get an irrelevant credentials lecture. The hint text
// is a fixed constant — it never interpolates err, so no secret can reach it.
func adminKeyHintFor(err error) string {
	var se *provider.StatusError
	if errors.As(err, &se) && (se.StatusCode == http.StatusUnauthorized || se.StatusCode == http.StatusForbidden) {
		return "\n" + adminKeyHint
	}
	if strings.Contains(err.Error(), "admin_key is empty") {
		return "\n" + adminKeyHint
	}
	return ""
}

// reportCmd fetches each enabled provider's usage for a period, persists the
// snapshots, aggregates and prints a table.
func (a *App) reportCmd() *cobra.Command {
	var (
		period  string
		byModel bool
	)

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Fetch usage & cost for a period and print a table",
		Long: "Fetch usage & cost from each enabled provider's admin API for the given\n" +
			"period, store the snapshots locally, and print an aggregated table.\n" +
			"\n" +
			adminKeyHelp,
		Example: "  aicost report                 # last 30 days (default)\n" +
			"  aicost report --period=7d     # last 7 days\n" +
			"  aicost report --period=month  # 1st of this month through today\n" +
			"  aicost report --period=today  # just today\n" +
			"  aicost report --period=month --by-model  # break down by (provider, model)",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runReport(cmd, period, byModel)
		},
	}
	cmd.Flags().StringVar(&period, "period", "30d", "period to report: Nd (e.g. 7d), month, or today")
	cmd.Flags().BoolVar(&byModel, "by-model", false, "break the table down by (provider, model)")
	return cmd
}

func (a *App) runReport(cmd *cobra.Command, period string, byModel bool) error {
	ctx := cmd.Context()

	cfg, err := a.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	window, err := parsePeriod(period, a.Now())
	if err != nil {
		return err
	}

	enabled := cfg.EnabledProviders()
	if len(enabled) == 0 {
		return fmt.Errorf("no providers enabled: enable one in config.yaml or set an AICOST_<PROVIDER>_ADMIN_KEY")
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

	var all []provider.UsageRecord
	for _, id := range enabled {
		src, err := a.NewProvider(id, cfg)
		if err != nil {
			return fmt.Errorf("init provider %s: %w", id, err)
		}
		snap, err := src.Fetch(ctx, window)
		if err != nil {
			return fmt.Errorf("fetch %s: %w%s", id, err, adminKeyHintFor(err))
		}
		if err := store.Save(ctx, snap); err != nil {
			return fmt.Errorf("save %s snapshot: %w", id, err)
		}
		all = append(all, snap.Records...)
	}

	if len(report.Aggregate(all)) == 0 {
		// report just fetched every enabled provider for this window, so an
		// empty result authoritatively means zero usage — not "not fetched".
		fmt.Fprintln(cmd.OutOrStdout(), "No usage data for this period.")
		return nil
	}

	out := cmd.OutOrStdout()
	if byModel {
		if err := report.ModelTable(out, report.AggregateByModel(all)); err != nil {
			return fmt.Errorf("render table: %w", err)
		}
	} else {
		if err := report.Table(out, report.Aggregate(all)); err != nil {
			return fmt.Errorf("render table: %w", err)
		}
	}
	return nil
}
