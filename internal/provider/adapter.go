package provider

import (
	"net/http"
	"sort"
	"time"
)

// Options configures a provider adapter. AdminKey is the admin/org-level key
// required by the usage/cost APIs (never a model-calling key). BaseURL overrides
// the provider's default host (for a proxy or httptest server). HTTPClient lets
// callers (and tests) inject a client with a specific timeout or transport; when
// nil a default with a bounded timeout is used. MaxRetries bounds the retry loop.
type Options struct {
	AdminKey   string
	BaseURL    string
	HTTPClient *http.Client
	MaxRetries int
}

// httpDoer returns the injected client or a default one with a per-request
// timeout, so no adapter ever issues an unbounded HTTP call.
func (o Options) httpDoer() httpDoer {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// dayModel is the aggregation key shared by cost and token maps: one UTC day and
// one model id.
type dayModel struct {
	day   time.Time // UTC midnight
	model string
}

// tokenCounts accumulates input/output token totals for a (day, model) key.
type tokenCounts struct {
	input  int64
	output int64
}

// mergeCostsAndTokens joins the cost map (USD per day/model) and the token map
// into a stable, sorted slice of UsageRecord. Keys present in either map produce
// a record; missing halves default to zero. Sorting keeps output deterministic
// for golden tests and idempotent storage upserts.
func mergeCostsAndTokens(providerID string, costs map[dayModel]float64, tokens map[dayModel]tokenCounts) []UsageRecord {
	keys := make(map[dayModel]struct{}, len(costs)+len(tokens))
	for k := range costs {
		keys[k] = struct{}{}
	}
	for k := range tokens {
		keys[k] = struct{}{}
	}

	records := make([]UsageRecord, 0, len(keys))
	for k := range keys {
		tc := tokens[k]
		records = append(records, UsageRecord{
			Provider:     providerID,
			Day:          k.day,
			Model:        k.model,
			InputTokens:  tc.input,
			OutputTokens: tc.output,
			CostUSD:      costs[k],
		})
	}

	sort.Slice(records, func(i, j int) bool {
		if !records[i].Day.Equal(records[j].Day) {
			return records[i].Day.Before(records[j].Day)
		}
		return records[i].Model < records[j].Model
	})
	return records
}

// parseUTCDay parses an RFC 3339 timestamp and truncates it to the start of the
// UTC day, the canonical Day value for a UsageRecord.
func parseUTCDay(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
}

// orDefault returns v when non-empty, otherwise def.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
