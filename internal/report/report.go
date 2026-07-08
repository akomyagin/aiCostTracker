// Package report turns usage records into human- and machine-readable output:
// a plain CLI table (Этап 1), then JSON and terminal trend charts / alerts
// (Фаза 2, see docs/POST_MVP_PLAN.md).
//
// Rendering is kept separate from fetching and storage so output formats can be
// golden-tested byte-for-byte without any network or DB.
//
// Реализация — Этап 1.
package report

import (
	"io"

	"github.com/akomyagin/aiCostTracker/internal/provider"
)

// Row is one aggregated line of a report: total spend/usage for a provider
// (optionally per model) over the reported window.
type Row struct {
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
}

// Aggregate collapses raw per-day records into report Rows grouped by provider
// (and model when present).
//
// Реализация — Этап 1.
func Aggregate(records []provider.UsageRecord) []Row {
	return nil
}

// Table writes rows as an aligned text table to w.
//
// Реализация — Этап 1.
func Table(w io.Writer, rows []Row) error {
	return nil
}
