package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// openaiDefaultBaseURL is the production OpenAI API host. Overridable via
// Options.BaseURL for a proxy or an httptest server in tests.
const openaiDefaultBaseURL = "https://api.openai.com"

// OpenAI is the ProviderUsageSource adapter for OpenAI's Usage + Costs Admin API.
// Cost (USD) comes from GET /v1/organization/costs and token counts from
// GET /v1/organization/usage/completions; both are day-bucketed and grouped by
// model, then merged on (day, model). See docs/API_NOTES.md §2.
//
// The Admin key (sk-admin-…) is distinct from a project sk- model key and is
// never logged.
type OpenAI struct {
	adminKey string // admin/org key; never logged
	baseURL  string
	client   *retryClient
}

var _ ProviderUsageSource = (*OpenAI)(nil)

// NewOpenAI builds the adapter from the given options. adminKey is the admin/org
// key required by the Usage API, not a normal model API key.
func NewOpenAI(opts Options) *OpenAI {
	return &OpenAI{
		adminKey: opts.AdminKey,
		baseURL:  strings.TrimRight(orDefault(opts.BaseURL, openaiDefaultBaseURL), "/"),
		client:   newRetryClient(opts.httpDoer(), opts.MaxRetries),
	}
}

// ID returns the stable provider identifier.
func (o *OpenAI) ID() string { return "openai" }

// Fetch retrieves usage + cost for the window and returns a normalized Snapshot.
func (o *OpenAI) Fetch(ctx context.Context, w Window) (Snapshot, error) {
	if o.adminKey == "" {
		return Snapshot{}, fmt.Errorf("provider openai: admin_key is empty (set AICOST_OPENAI_ADMIN_KEY or config)")
	}

	costs, err := o.fetchCosts(ctx, w)
	if err != nil {
		return Snapshot{}, fmt.Errorf("provider openai: costs: %w", err)
	}
	tokens, err := o.fetchTokens(ctx, w)
	if err != nil {
		return Snapshot{}, fmt.Errorf("provider openai: usage completions: %w", err)
	}

	records := mergeCostsAndTokens(o.ID(), costs, tokens)
	return Snapshot{
		Provider:  o.ID(),
		Window:    w,
		Records:   records,
		FetchedAt: time.Now().UTC(),
	}, nil
}

// --- costs ---

type openaiCostResponse struct {
	Data []struct {
		StartTime int64 `json:"start_time"` // Unix seconds
		Results   []struct {
			Amount struct {
				Value    float64 `json:"value"`    // USD dollars
				Currency string  `json:"currency"` // "usd"
			} `json:"amount"`
			LineItem string `json:"line_item"` // e.g. "gpt-4o-2024-08-06, input"
		} `json:"results"`
	} `json:"data"`
	HasMore  bool   `json:"has_more"`
	NextPage string `json:"next_page"`
}

func (o *OpenAI) fetchCosts(ctx context.Context, w Window) (map[dayModel]float64, error) {
	out := make(map[dayModel]float64)
	page := ""

	for pages := 0; ; pages++ {
		if pages >= maxPaginationPages {
			return nil, errTooManyPages("costs")
		}
		q := url.Values{}
		q.Set("start_time", strconv.FormatInt(w.Start.UTC().Unix(), 10))
		q.Set("end_time", strconv.FormatInt(w.End.UTC().Unix(), 10))
		q.Set("bucket_width", "1d")
		q.Add("group_by[]", "line_item")
		q.Set("limit", "180") // 1d buckets; cover a wide window in one page
		if page != "" {
			q.Set("page", page)
		}
		endpoint := o.baseURL + "/v1/organization/costs?" + q.Encode()

		raw, err := o.client.doJSON(ctx, func(ctx context.Context) (*http.Request, error) {
			return o.newRequest(ctx, endpoint)
		})
		if err != nil {
			return nil, err
		}

		var resp openaiCostResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("decode costs: %w", err)
		}
		for _, bucket := range resp.Data {
			day := unixToUTCDay(bucket.StartTime)
			for _, r := range bucket.Results {
				out[dayModel{day: day, model: modelFromLineItem(r.LineItem)}] += r.Amount.Value
			}
		}
		if !resp.HasMore || resp.NextPage == "" {
			break
		}
		page = resp.NextPage
	}
	return out, nil
}

// --- usage/completions ---

type openaiUsageResponse struct {
	Data []struct {
		StartTime int64 `json:"start_time"`
		Results   []struct {
			InputTokens  int64  `json:"input_tokens"`
			OutputTokens int64  `json:"output_tokens"`
			Model        string `json:"model"`
		} `json:"results"`
	} `json:"data"`
	HasMore  bool   `json:"has_more"`
	NextPage string `json:"next_page"`
}

func (o *OpenAI) fetchTokens(ctx context.Context, w Window) (map[dayModel]tokenCounts, error) {
	out := make(map[dayModel]tokenCounts)
	page := ""

	for pages := 0; ; pages++ {
		if pages >= maxPaginationPages {
			return nil, errTooManyPages("usage/completions")
		}
		q := url.Values{}
		q.Set("start_time", strconv.FormatInt(w.Start.UTC().Unix(), 10))
		q.Set("end_time", strconv.FormatInt(w.End.UTC().Unix(), 10))
		q.Set("bucket_width", "1d")
		q.Add("group_by[]", "model")
		q.Set("limit", "180")
		if page != "" {
			q.Set("page", page)
		}
		endpoint := o.baseURL + "/v1/organization/usage/completions?" + q.Encode()

		raw, err := o.client.doJSON(ctx, func(ctx context.Context) (*http.Request, error) {
			return o.newRequest(ctx, endpoint)
		})
		if err != nil {
			return nil, err
		}

		var resp openaiUsageResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("decode usage completions: %w", err)
		}
		for _, bucket := range resp.Data {
			day := unixToUTCDay(bucket.StartTime)
			for _, r := range bucket.Results {
				k := dayModel{day: day, model: r.Model}
				tc := out[k]
				tc.input += r.InputTokens
				tc.output += r.OutputTokens
				out[k] = tc
			}
		}
		if !resp.HasMore || resp.NextPage == "" {
			break
		}
		page = resp.NextPage
	}
	return out, nil
}

// newRequest builds a GET request with OpenAI admin bearer auth.
func (o *OpenAI) newRequest(ctx context.Context, endpoint string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.adminKey)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// modelFromLineItem extracts the model id from a costs line_item such as
// "gpt-4o-2024-08-06, input" → "gpt-4o-2024-08-06". Returns the whole string when
// it has no comma (or "" when empty), so cost is never silently dropped.
func modelFromLineItem(item string) string {
	if item == "" {
		return ""
	}
	if i := strings.IndexByte(item, ','); i >= 0 {
		return strings.TrimSpace(item[:i])
	}
	return strings.TrimSpace(item)
}

// unixToUTCDay truncates a Unix-seconds timestamp to the start of its UTC day.
func unixToUTCDay(sec int64) time.Time {
	t := time.Unix(sec, 0).UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
