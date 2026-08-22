// Package report turns usage records into human- and machine-readable output:
// a plain CLI table (Этап 1), then JSON and terminal trend charts / alerts
// (Фаза 2, see docs/POST_MVP_PLAN.md).
//
// Rendering is kept separate from fetching and storage so output formats can be
// golden-tested byte-for-byte without any network or DB.
package report

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/akomyagin/aiCostTracker/internal/provider"
)

// Row is one aggregated line of a report: total spend/usage for a provider over
// the reported window. Per-model breakdown is a Фаза-2 concern; MVP rolls each
// provider's records into a single row plus a grand total.
type Row struct {
	Provider     string
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
}

// Aggregate collapses raw per-day records into one Row per provider, summing
// tokens and cost across all days and models. Rows are sorted by provider id for
// deterministic output. Records with an empty provider are ignored.
func Aggregate(records []provider.UsageRecord) []Row {
	byProvider := map[string]*Row{}
	for _, r := range records {
		if r.Provider == "" {
			continue
		}
		row, ok := byProvider[r.Provider]
		if !ok {
			row = &Row{Provider: r.Provider}
			byProvider[r.Provider] = row
		}
		row.InputTokens += r.InputTokens
		row.OutputTokens += r.OutputTokens
		row.CostUSD += r.CostUSD
	}

	rows := make([]Row, 0, len(byProvider))
	for _, row := range byProvider {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Provider < rows[j].Provider })
	return rows
}

// Table writes rows as an aligned text table to w, with a TOTAL footer summing
// every provider. Output is deterministic (rows must already be sorted by
// Aggregate) so it can be golden-tested byte-for-byte.
func Table(w io.Writer, rows []Row) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	if _, err := fmt.Fprintln(tw, "PROVIDER\tINPUT TOKENS\tOUTPUT TOKENS\tCOST (USD)"); err != nil {
		return err
	}

	var (
		totalIn, totalOut int64
		totalCost         float64
	)
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n",
			r.Provider, r.InputTokens, r.OutputTokens, formatUSD(r.CostUSD)); err != nil {
			return err
		}
		totalIn += r.InputTokens
		totalOut += r.OutputTokens
		totalCost += r.CostUSD
	}

	if _, err := fmt.Fprintf(tw, "TOTAL\t%d\t%d\t%s\n",
		totalIn, totalOut, formatUSD(totalCost)); err != nil {
		return err
	}

	return tw.Flush()
}

// ModelRow is one aggregated line of a per-model report: total spend/usage for
// a (provider, model) pair over the reported window.
type ModelRow struct {
	Provider     string
	Model        string // "" if the provider did not report a model
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
}

// AggregateByModel collapses raw per-day records into one ModelRow per
// (provider, model) pair. Rows are sorted by provider id, then model, for
// deterministic output. Records with an empty provider are ignored; an empty
// model is kept as its own bucket (rendered as "(unknown)" by ModelTable).
func AggregateByModel(records []provider.UsageRecord) []ModelRow {
	byKey := map[[2]string]*ModelRow{}
	for _, r := range records {
		if r.Provider == "" {
			continue
		}
		key := [2]string{r.Provider, r.Model}
		row, ok := byKey[key]
		if !ok {
			row = &ModelRow{Provider: r.Provider, Model: r.Model}
			byKey[key] = row
		}
		row.InputTokens += r.InputTokens
		row.OutputTokens += r.OutputTokens
		row.CostUSD += r.CostUSD
	}

	rows := make([]ModelRow, 0, len(byKey))
	for _, row := range byKey {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Provider != rows[j].Provider {
			return rows[i].Provider < rows[j].Provider
		}
		return rows[i].Model < rows[j].Model
	})
	return rows
}

// ModelTable writes per-model rows as an aligned text table with a TOTAL footer.
// An empty model is rendered as "(unknown)". Output is deterministic (rows must
// already be sorted by AggregateByModel) so it can be golden-tested byte-for-byte.
func ModelTable(w io.Writer, rows []ModelRow) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	if _, err := fmt.Fprintln(tw, "PROVIDER\tMODEL\tINPUT TOKENS\tOUTPUT TOKENS\tCOST (USD)"); err != nil {
		return err
	}

	var (
		totalIn, totalOut int64
		totalCost         float64
	)
	for _, r := range rows {
		model := r.Model
		if model == "" {
			model = "(unknown)"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n",
			r.Provider, model, r.InputTokens, r.OutputTokens, formatUSD(r.CostUSD)); err != nil {
			return err
		}
		totalIn += r.InputTokens
		totalOut += r.OutputTokens
		totalCost += r.CostUSD
	}

	if _, err := fmt.Fprintf(tw, "TOTAL\t\t%d\t%d\t%s\n",
		totalIn, totalOut, formatUSD(totalCost)); err != nil {
		return err
	}

	return tw.Flush()
}

// formatUSD renders a dollar amount with a leading $ and two decimals.
func formatUSD(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}
