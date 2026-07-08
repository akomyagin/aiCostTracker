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

// anthropicDefaultBaseURL is the production Anthropic API host. Overridable via
// Options.BaseURL for a proxy or an httptest server in tests.
const anthropicDefaultBaseURL = "https://api.anthropic.com"

// anthropicVersion is the required API version header value for the Admin API.
const anthropicVersion = "2023-06-01"

// Anthropic is the ProviderUsageSource adapter for Anthropic's Usage & Cost
// Admin API. Cost (USD) comes from GET /v1/organizations/cost_report and token
// counts from GET /v1/organizations/usage_report/messages; both are day-bucketed
// and grouped by model, then merged on (day, model). See docs/API_NOTES.md §1.
//
// The Admin key (sk-ant-admin…) is distinct from a model-calling API key and is
// never logged.
type Anthropic struct {
	adminKey string // org/admin-level key; never logged
	baseURL  string
	client   *retryClient
}

var _ ProviderUsageSource = (*Anthropic)(nil)

// NewAnthropic builds the adapter from the given options. adminKey is the
// admin/org key required by the Usage & Cost Admin API, not the model API key.
func NewAnthropic(opts Options) *Anthropic {
	return &Anthropic{
		adminKey: opts.AdminKey,
		baseURL:  strings.TrimRight(orDefault(opts.BaseURL, anthropicDefaultBaseURL), "/"),
		client:   newRetryClient(opts.httpDoer(), opts.MaxRetries),
	}
}

// ID returns the stable provider identifier.
func (a *Anthropic) ID() string { return "anthropic" }

// Fetch retrieves usage + cost for the window and returns a normalized Snapshot.
func (a *Anthropic) Fetch(ctx context.Context, w Window) (Snapshot, error) {
	if a.adminKey == "" {
		return Snapshot{}, fmt.Errorf("provider anthropic: admin_key is empty (set AICOST_ANTHROPIC_ADMIN_KEY or config)")
	}

	costs, err := a.fetchCosts(ctx, w)
	if err != nil {
		return Snapshot{}, fmt.Errorf("provider anthropic: cost report: %w", err)
	}
	tokens, err := a.fetchTokens(ctx, w)
	if err != nil {
		return Snapshot{}, fmt.Errorf("provider anthropic: usage report: %w", err)
	}

	records := mergeCostsAndTokens(a.ID(), costs, tokens)
	return Snapshot{
		Provider:  a.ID(),
		Window:    w,
		Records:   records,
		FetchedAt: time.Now().UTC(),
	}, nil
}

// --- cost_report ---

type anthropicCostResponse struct {
	Data []struct {
		StartingAt string `json:"starting_at"`
		Results    []struct {
			Amount string `json:"amount"` // decimal string in USD dollars
			Model  string `json:"model"`  // null when not grouped by description
		} `json:"results"`
	} `json:"data"`
	HasMore  bool   `json:"has_more"`
	NextPage string `json:"next_page"`
}

func (a *Anthropic) fetchCosts(ctx context.Context, w Window) (map[dayModel]float64, error) {
	out := make(map[dayModel]float64)
	page := ""

	for {
		q := url.Values{}
		q.Set("starting_at", w.Start.UTC().Format(time.RFC3339))
		q.Set("ending_at", w.End.UTC().Format(time.RFC3339))
		q.Set("bucket_width", "1d")
		q.Add("group_by[]", "description")
		if page != "" {
			q.Set("page", page)
		}
		endpoint := a.baseURL + "/v1/organizations/cost_report?" + q.Encode()

		raw, err := a.client.doJSON(ctx, func(ctx context.Context) (*http.Request, error) {
			return a.newRequest(ctx, endpoint)
		})
		if err != nil {
			return nil, err
		}

		var resp anthropicCostResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("decode cost report: %w", err)
		}
		for _, bucket := range resp.Data {
			day, err := parseUTCDay(bucket.StartingAt)
			if err != nil {
				return nil, fmt.Errorf("cost bucket starting_at %q: %w", bucket.StartingAt, err)
			}
			for _, r := range bucket.Results {
				amt, err := strconv.ParseFloat(r.Amount, 64)
				if err != nil {
					return nil, fmt.Errorf("cost amount %q: %w", r.Amount, err)
				}
				out[dayModel{day: day, model: r.Model}] += amt
			}
		}
		if !resp.HasMore || resp.NextPage == "" {
			break
		}
		page = resp.NextPage
	}
	return out, nil
}

// --- usage_report/messages ---

type anthropicUsageResponse struct {
	Data []struct {
		StartingAt string `json:"starting_at"`
		Results    []struct {
			UncachedInputTokens  int64  `json:"uncached_input_tokens"`
			CacheReadInputTokens int64  `json:"cache_read_input_tokens"`
			OutputTokens         int64  `json:"output_tokens"`
			Model                string `json:"model"`
		} `json:"results"`
	} `json:"data"`
	HasMore  bool   `json:"has_more"`
	NextPage string `json:"next_page"`
}

func (a *Anthropic) fetchTokens(ctx context.Context, w Window) (map[dayModel]tokenCounts, error) {
	out := make(map[dayModel]tokenCounts)
	page := ""

	for {
		q := url.Values{}
		q.Set("starting_at", w.Start.UTC().Format(time.RFC3339))
		q.Set("ending_at", w.End.UTC().Format(time.RFC3339))
		q.Set("bucket_width", "1d")
		q.Add("group_by[]", "model")
		if page != "" {
			q.Set("page", page)
		}
		endpoint := a.baseURL + "/v1/organizations/usage_report/messages?" + q.Encode()

		raw, err := a.client.doJSON(ctx, func(ctx context.Context) (*http.Request, error) {
			return a.newRequest(ctx, endpoint)
		})
		if err != nil {
			return nil, err
		}

		var resp anthropicUsageResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("decode usage report: %w", err)
		}
		for _, bucket := range resp.Data {
			day, err := parseUTCDay(bucket.StartingAt)
			if err != nil {
				return nil, fmt.Errorf("usage bucket starting_at %q: %w", bucket.StartingAt, err)
			}
			for _, r := range bucket.Results {
				k := dayModel{day: day, model: r.Model}
				tc := out[k]
				// Full input = uncached + cache reads (billed input surface).
				tc.input += r.UncachedInputTokens + r.CacheReadInputTokens
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

// newRequest builds a GET request with Anthropic admin auth headers.
func (a *Anthropic) newRequest(ctx context.Context, endpoint string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", a.adminKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Accept", "application/json")
	return req, nil
}
