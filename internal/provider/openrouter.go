package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// openrouterDefaultBaseURL is the production OpenRouter host. Overridable via
// Options.BaseURL for a proxy or an httptest server in tests.
const openrouterDefaultBaseURL = "https://openrouter.ai"

// openrouterQueryLimit caps rows returned by a single analytics query. The API
// has no cursor pagination (only limit + metadata.truncated), so a truncated
// response is a hard error rather than a follow-up page (see Fetch).
const openrouterQueryLimit = 1000

// OpenRouter is the ProviderUsageSource adapter for OpenRouter's Analytics API.
// Cost (USD) and token counts come from a single POST /api/v1/analytics/query
// grouped by model with day granularity. See docs/API_NOTES.md §3.
//
// The management key (Settings -> Management Keys) is distinct from a normal
// inference key (which gets 403 on this endpoint) and is never logged.
type OpenRouter struct {
	adminKey string // management key; never logged
	baseURL  string
	client   *retryClient
}

var _ ProviderUsageSource = (*OpenRouter)(nil)

// NewOpenRouter builds the adapter from the given options. adminKey is the
// management key required by the Analytics API, not a normal inference key.
func NewOpenRouter(opts Options) *OpenRouter {
	return &OpenRouter{
		adminKey: opts.AdminKey,
		baseURL:  strings.TrimRight(orDefault(opts.BaseURL, openrouterDefaultBaseURL), "/"),
		client:   newRetryClient(opts.httpDoer(), opts.MaxRetries),
	}
}

// ID returns the stable provider identifier.
func (o *OpenRouter) ID() string { return "openrouter" }

// openrouterQueryRequest is the analytics query body. [ASSUMPTION] the exact
// metric names — see the Metrics literal in Fetch and docs/API_NOTES.md §3.
type openrouterQueryRequest struct {
	Metrics     []string            `json:"metrics"`
	Dimensions  []string            `json:"dimensions"`  // max 2 per API; we send ["model"]
	Granularity string              `json:"granularity"` // "day"
	TimeRange   openrouterTimeRange `json:"time_range"`
	Limit       int                 `json:"limit"` // openrouterQueryLimit = 1000
}

type openrouterTimeRange struct {
	Start string `json:"start"` // w.Start.UTC().Format(time.RFC3339)
	End   string `json:"end"`   // w.End.UTC().Format(time.RFC3339)
}

type openrouterQueryResponse struct {
	Data struct {
		Data     []openrouterRow `json:"data"`
		Metadata struct {
			RowCount  int  `json:"row_count"`
			Truncated bool `json:"truncated"`
		} `json:"metadata"`
	} `json:"data"`
}

// openrouterRow tolerates the [ASSUMPTION] about the exact date-field name by
// declaring all documented candidates; day() picks the first non-empty one.
type openrouterRow struct {
	Date         string  `json:"date"`
	DateDay      string  `json:"date__day"`
	CreatedDay   string  `json:"created_at__day"`
	Model        string  `json:"model"`
	TotalUsage   float64 `json:"total_usage"`       // USD
	PromptToks   int64   `json:"prompt_tokens"`     // [ASSUMPTION] may be absent -> 0
	CompleteToks int64   `json:"completion_tokens"` // [ASSUMPTION] may be absent -> 0
}

// day returns the UTC-midnight day this row accounts for. [ASSUMPTION] the exact
// date-field name is one of date / date__day / created_at__day (checked in that
// order); the value may be an RFC3339 timestamp or a bare YYYY-MM-DD date. An
// empty value in every candidate is a decode error, never a silently skipped row.
func (r openrouterRow) day() (time.Time, error) {
	s := r.Date
	if s == "" {
		s = r.DateDay
	}
	if s == "" {
		s = r.CreatedDay
	}
	if s == "" {
		return time.Time{}, fmt.Errorf("row has no date field (date/date__day/created_at__day all empty)")
	}
	// Bare date first (the granularity is "day"), then RFC3339 fallback.
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	return parseUTCDay(s)
}

// Fetch retrieves usage + cost for the window and returns a normalized Snapshot.
func (o *OpenRouter) Fetch(ctx context.Context, w Window) (Snapshot, error) {
	if o.adminKey == "" {
		return Snapshot{}, fmt.Errorf("provider openrouter: admin_key is empty (set AICOST_OPENROUTER_ADMIN_KEY or config)")
	}

	// [ASSUMPTION] metric names. total_usage is the USD cost; prompt_tokens /
	// completion_tokens are assumed to exist. If the live API returns 400 on an
	// unknown metric, narrow to total_usage and leave tokens zero (documented
	// limitation in docs/API_NOTES.md §3).
	reqBody := openrouterQueryRequest{
		Metrics:     []string{"total_usage", "prompt_tokens", "completion_tokens"},
		Dimensions:  []string{"model"},
		Granularity: "day",
		TimeRange: openrouterTimeRange{
			Start: w.Start.UTC().Format(time.RFC3339),
			End:   w.End.UTC().Format(time.RFC3339),
		},
		Limit: openrouterQueryLimit,
	}
	// Marshal once; rebuild a fresh reader per attempt so the body survives retry.
	body, err := json.Marshal(reqBody)
	if err != nil {
		return Snapshot{}, fmt.Errorf("provider openrouter: analytics query: %w", err)
	}

	endpoint := o.baseURL + "/api/v1/analytics/query"
	raw, err := o.client.doJSON(ctx, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+o.adminKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		return req, nil
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("provider openrouter: analytics query: %w", err)
	}

	var resp openrouterQueryResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Snapshot{}, fmt.Errorf("provider openrouter: analytics query: decode: %w", err)
	}

	// No cursor exists to fetch the tail, so a truncated response would silently
	// under-report spend. Fail loudly instead. This is the pagination-less analog
	// of errTooManyPages; maxPaginationPages is deliberately not used here (single
	// request, no loop).
	if resp.Data.Metadata.Truncated {
		return Snapshot{}, fmt.Errorf("provider openrouter: analytics query: openrouter analytics response truncated at %d rows (limit %d): narrow the report period",
			resp.Data.Metadata.RowCount, openrouterQueryLimit)
	}

	costs := make(map[dayModel]float64, len(resp.Data.Data))
	tokens := make(map[dayModel]tokenCounts, len(resp.Data.Data))
	for _, row := range resp.Data.Data {
		day, err := row.day()
		if err != nil {
			return Snapshot{}, fmt.Errorf("provider openrouter: analytics query: %w", err)
		}
		k := dayModel{day: day, model: row.Model}
		costs[k] += row.TotalUsage
		tc := tokens[k]
		tc.input += row.PromptToks
		tc.output += row.CompleteToks
		tokens[k] = tc
	}

	records := mergeCostsAndTokens(o.ID(), costs, tokens)
	return Snapshot{
		Provider:  o.ID(),
		Window:    w,
		Records:   records,
		FetchedAt: time.Now().UTC(),
	}, nil
}
