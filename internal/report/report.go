// Package report turns usage records into human- and machine-readable output:
// a plain CLI table (Этап 1), then JSON and terminal trend charts / alerts
// (Фаза 2, see docs/POST_MVP_PLAN.md).
//
// Rendering is kept separate from fetching and storage so output formats can be
// golden-tested byte-for-byte without any network or DB.
package report

import (
	"encoding/json"
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
	CostMicros   int64 // integer micro-USD (1 USD = 1e6); serialized as float64 dollars only via jsonRow
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
		row.CostMicros += r.CostMicros
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
		totalCost         int64
	)
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n",
			r.Provider, r.InputTokens, r.OutputTokens, formatUSD(r.CostMicros)); err != nil {
			return err
		}
		totalIn += r.InputTokens
		totalOut += r.OutputTokens
		totalCost += r.CostMicros
	}

	if _, err := fmt.Fprintf(tw, "TOTAL\t%d\t%d\t%s\n",
		totalIn, totalOut, formatUSD(totalCost)); err != nil {
		return err
	}

	return tw.Flush()
}

// jsonRow is the wire shape of one row of the --format=json document. It exists
// separately from Row because the JSON contract (§P4, schema_version 1) exposes
// cost as float64 dollars under "cost_usd", while Row now carries integer
// micro-dollars internally.
type jsonRow struct {
	Provider     string  `json:"provider"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// jsonTotal is the grand-total block of the JSON document: the same numeric
// fields as jsonRow but without a provider, summed across every row.
type jsonTotal struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// jsonDoc is the machine-readable contract written by JSON. Field order here is
// the field order in the output (encoding/json preserves struct order).
type jsonDoc struct {
	SchemaVersion int       `json:"schema_version"`
	Rows          []jsonRow `json:"rows"`
	Total         jsonTotal `json:"total"`
}

// JSON writes rows as a machine-readable schema_version-tagged document.
// schema_version starts at 1; bump it (not the shape) on any future breaking
// change to this contract, per docs/POST_MVP_PLAN.md §P4.
//
// The document mirrors the default Table: one Row per provider (sorted by
// Aggregate) plus a grand total. rows serializes as [] (never null) on empty
// input. Output is deterministic so it can be golden-tested byte-for-byte.
func JSON(w io.Writer, rows []Row) error {
	// Build the wire rows in a freshly allocated slice so a nil input still
	// marshals to [] rather than null, keeping the contract stable for machine
	// consumers. Cost is converted from micro-USD to float64 dollars here, once
	// per row (§P4: "cost_usd" is dollars).
	out := make([]jsonRow, 0, len(rows))
	var totalMicros int64
	for _, r := range rows {
		out = append(out, jsonRow{
			Provider:     r.Provider,
			InputTokens:  r.InputTokens,
			OutputTokens: r.OutputTokens,
			CostUSD:      provider.MicrosToDollars(r.CostMicros),
		})
		totalMicros += r.CostMicros
	}

	var total jsonTotal
	for _, r := range rows {
		total.InputTokens += r.InputTokens
		total.OutputTokens += r.OutputTokens
	}
	// Sum cost in int64 micros and convert once, so the total never accumulates
	// float rounding error (which is the whole point of the micros migration).
	total.CostUSD = provider.MicrosToDollars(totalMicros)

	doc := jsonDoc{SchemaVersion: 1, Rows: out, Total: total}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

// ModelRow is one aggregated line of a per-model report: total spend/usage for
// a (provider, model) pair over the reported window.
type ModelRow struct {
	Provider     string
	Model        string // "" if the provider did not report a model
	InputTokens  int64
	OutputTokens int64
	CostMicros   int64 // integer micro-USD (1 USD = 1e6)
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
		row.CostMicros += r.CostMicros
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
		totalCost         int64
	)
	for _, r := range rows {
		model := r.Model
		if model == "" {
			model = "(unknown)"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n",
			r.Provider, model, r.InputTokens, r.OutputTokens, formatUSD(r.CostMicros)); err != nil {
			return err
		}
		totalIn += r.InputTokens
		totalOut += r.OutputTokens
		totalCost += r.CostMicros
	}

	if _, err := fmt.Fprintf(tw, "TOTAL\t\t%d\t%d\t%s\n",
		totalIn, totalOut, formatUSD(totalCost)); err != nil {
		return err
	}

	return tw.Flush()
}

// formatUSD renders a micro-dollar amount with a leading $ and two decimals.
// Conversion goes through float64 + Sprintf("%.2f") deliberately: it keeps
// rounding byte-identical to the pre-micros renderer for every existing golden
// fixture. Integer cent rounding ((m+5000)/10000) would be half-up, whereas
// %.2f is IEEE round-half-even — e.g. $0.125 renders as $0.12, not $0.13.
func formatUSD(m int64) string {
	return fmt.Sprintf("$%.2f", provider.MicrosToDollars(m))
}
