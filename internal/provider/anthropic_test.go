package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// realistic response bodies copied from docs/API_NOTES.md §1 (which quotes the
// live Anthropic Admin API docs), so parsing is validated against the real shape.

const anthropicCostBody = `{
  "data": [
    {
      "ending_at": "2025-08-02T00:00:00Z",
      "results": [
        {
          "amount": "1.50",
          "currency": "USD",
          "cost_type": "tokens",
          "description": "Claude Opus Usage - Input Tokens",
          "model": "claude-opus-4-6",
          "token_type": "uncached_input_tokens",
          "context_window": "0-200k",
          "service_tier": "standard",
          "workspace_id": "wrkspc_01"
        },
        {
          "amount": "0.75",
          "currency": "USD",
          "cost_type": "tokens",
          "description": "Claude Opus Usage - Output Tokens",
          "model": "claude-opus-4-6",
          "token_type": "output_tokens",
          "context_window": "0-200k",
          "service_tier": "standard",
          "workspace_id": "wrkspc_01"
        }
      ],
      "starting_at": "2025-08-01T00:00:00Z"
    }
  ],
  "has_more": false,
  "next_page": null
}`

const anthropicUsageBody = `{
  "data": [
    {
      "ending_at": "2025-08-02T00:00:00Z",
      "results": [
        {
          "account_id": null,
          "api_key_id": null,
          "cache_creation": { "ephemeral_1h_input_tokens": 100, "ephemeral_5m_input_tokens": 50 },
          "cache_read_input_tokens": 200,
          "context_window": "0-200k",
          "model": "claude-opus-4-6",
          "output_tokens": 500,
          "server_tool_use": { "web_search_requests": 0 },
          "service_tier": "standard",
          "uncached_input_tokens": 1500,
          "workspace_id": "wrkspc_01"
        }
      ],
      "starting_at": "2025-08-01T00:00:00Z"
    }
  ],
  "has_more": false,
  "next_page": null
}`

// newAnthropicTestAdapter wires an adapter to a test server URL with retries off
// so a single fatal/network case doesn't loop.
func newAnthropicTestAdapter(t *testing.T, baseURL string, maxRetries int) *Anthropic {
	t.Helper()
	return NewAnthropic(Options{
		AdminKey:   "sk-ant-admin-SECRET",
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		MaxRetries: maxRetries,
	})
}

func TestAnthropicFetch_MergesCostAndTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/cost_report"):
			_, _ = w.Write([]byte(anthropicCostBody))
		case strings.Contains(r.URL.Path, "/usage_report/messages"):
			_, _ = w.Write([]byte(anthropicUsageBody))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	a := newAnthropicTestAdapter(t, srv.URL, 0)
	w := Window{
		Start: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2025, 8, 2, 0, 0, 0, 0, time.UTC),
	}

	snap, err := a.Fetch(context.Background(), w)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", snap.Provider)
	}
	if len(snap.Records) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(snap.Records), snap.Records)
	}
	rec := snap.Records[0]
	wantDay := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	if !rec.Day.Equal(wantDay) {
		t.Errorf("Day = %v, want %v", rec.Day, wantDay)
	}
	if rec.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q", rec.Model)
	}
	// input = uncached (1500) + cache read (200).
	if rec.InputTokens != 1700 {
		t.Errorf("InputTokens = %d, want 1700", rec.InputTokens)
	}
	if rec.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500", rec.OutputTokens)
	}
	// cost = 1.50 + 0.75 = 2.25 exactly (micros make the tolerance unnecessary).
	if rec.CostMicros != 2_250_000 {
		t.Errorf("CostMicros = %d, want 2_250_000 ($2.25)", rec.CostMicros)
	}
}

// TestAnthropicFetch_CostAccumulationIsExact is the money-precision regression
// test (Этап 9): three cost_report pages each report amount "0.1" for the SAME
// (day, model), so the adapter accumulates 0.1 + 0.1 + 0.1. In float64 that sum
// is 0.30000000000000004; as integer micro-USD it is exactly 300_000. The exact
// equality below would have failed under the old float64 CostUSD field.
func TestAnthropicFetch_CostAccumulationIsExact(t *testing.T) {
	var costHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/cost_report"):
			costHits++
			hasMore := costHits < 3
			nextPage := "null"
			if hasMore {
				nextPage = fmt.Sprintf("%q", fmt.Sprintf("cursor-%d", costHits))
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{
  "data": [
    {
      "starting_at": "2025-08-01T00:00:00Z",
      "ending_at": "2025-08-02T00:00:00Z",
      "results": [ { "amount": "0.1", "currency": "USD", "model": "claude-opus-4-6" } ]
    }
  ],
  "has_more": %t,
  "next_page": %s
}`, hasMore, nextPage)))
		case strings.Contains(r.URL.Path, "/usage_report/messages"):
			_, _ = w.Write([]byte(`{"data":[],"has_more":false,"next_page":null}`))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	a := newAnthropicTestAdapter(t, srv.URL, 0)
	w := Window{
		Start: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2025, 8, 2, 0, 0, 0, 0, time.UTC),
	}
	snap, err := a.Fetch(context.Background(), w)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if costHits != 3 {
		t.Fatalf("cost_report hit %d times, want 3 pages", costHits)
	}
	if len(snap.Records) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(snap.Records), snap.Records)
	}
	if snap.Records[0].CostMicros != 300_000 {
		t.Errorf("CostMicros = %d, want 300_000 (0.1+0.1+0.1 exact)", snap.Records[0].CostMicros)
	}
}

func TestAnthropicFetch_RejectsNonFiniteAmount(t *testing.T) {
	// strconv.ParseFloat accepts the spellings "NaN"/"Inf" without error;
	// converting either to int64 money would silently corrupt the total, so a
	// malformed/hostile response must fail loudly instead.
	for _, amount := range []string{"NaN", "Inf", "-Inf"} {
		t.Run(amount, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, "/cost_report"):
					_, _ = w.Write([]byte(fmt.Sprintf(`{
  "data": [
    {
      "starting_at": "2025-08-01T00:00:00Z",
      "ending_at": "2025-08-02T00:00:00Z",
      "results": [ { "amount": %q, "currency": "USD", "model": "claude-opus-4-6" } ]
    }
  ],
  "has_more": false,
  "next_page": null
}`, amount)))
				case strings.Contains(r.URL.Path, "/usage_report/messages"):
					_, _ = w.Write([]byte(`{"data":[],"has_more":false,"next_page":null}`))
				default:
					http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
				}
			}))
			defer srv.Close()

			a := newAnthropicTestAdapter(t, srv.URL, 0)
			w := Window{
				Start: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC),
				End:   time.Date(2025, 8, 2, 0, 0, 0, 0, time.UTC),
			}
			_, err := a.Fetch(context.Background(), w)
			if err == nil {
				t.Fatalf("Fetch with amount %q: want error, got nil", amount)
			}
			if !strings.Contains(err.Error(), "not a finite number") {
				t.Errorf("Fetch error = %q, want mention of a finite-number check", err.Error())
			}
		})
	}
}

// TestAnthropicFetchCosts_PaginationLoopIsBounded guards against a
// malfunctioning/hostile endpoint that always answers has_more=true: without a
// page cap this would loop forever (found by independent /code-review on Этап
// 1). The fake server always returns has_more=true with a fresh next_page
// cursor, so the loop can only stop via maxPaginationPages.
func TestAnthropicFetchCosts_PaginationLoopIsBounded(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(fmt.Sprintf(`{"data":[],"has_more":true,"next_page":"cursor-%d"}`, calls)))
	}))
	defer srv.Close()

	a := newAnthropicTestAdapter(t, srv.URL, 0)
	w := Window{
		Start: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2025, 8, 2, 0, 0, 0, 0, time.UTC),
	}

	_, err := a.Fetch(context.Background(), w)
	if err == nil {
		t.Fatal("expected an error once the pagination cap is exceeded")
	}
	if calls != maxPaginationPages {
		t.Errorf("calls = %d, want exactly maxPaginationPages (%d)", calls, maxPaginationPages)
	}
}

func TestAnthropicFetch_RetriesThenSucceeds(t *testing.T) {
	var costHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/cost_report"):
			costHits++
			if costHits == 1 {
				http.Error(w, `{"error":"slow down"}`, http.StatusTooManyRequests) // retryable
				return
			}
			_, _ = w.Write([]byte(anthropicCostBody))
		case strings.Contains(r.URL.Path, "/usage_report/messages"):
			_, _ = w.Write([]byte(anthropicUsageBody))
		}
	}))
	defer srv.Close()

	a := newAnthropicTestAdapter(t, srv.URL, 3)
	a.client.baseDelay = time.Millisecond // keep the test fast

	w := Window{
		Start: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2025, 8, 2, 0, 0, 0, 0, time.UTC),
	}
	snap, err := a.Fetch(context.Background(), w)
	if err != nil {
		t.Fatalf("Fetch after retry: %v", err)
	}
	if costHits != 2 {
		t.Errorf("cost endpoint hit %d times, want 2 (429 then 200)", costHits)
	}
	if len(snap.Records) != 1 {
		t.Fatalf("want 1 record after retry, got %d", len(snap.Records))
	}
}

func TestAnthropicFetch_FatalStatusNoRetry(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, `{"error":"invalid admin key"}`, http.StatusUnauthorized) // fatal
	}))
	defer srv.Close()

	a := newAnthropicTestAdapter(t, srv.URL, 5)
	a.client.baseDelay = time.Millisecond

	_, err := a.Fetch(context.Background(), Window{
		Start: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2025, 8, 2, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if hits != 1 {
		t.Errorf("fatal 401 was retried: %d hits, want 1", hits)
	}
}

// TestAnthropicFetch_KeyNotLeaked asserts the admin key never appears in error
// output — the required secret test from the plan.
func TestAnthropicFetch_KeyNotLeaked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	}))
	defer srv.Close()

	a := newAnthropicTestAdapter(t, srv.URL, 0)
	_, err := a.Fetch(context.Background(), Window{
		Start: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2025, 8, 2, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "sk-ant-admin-SECRET") {
		t.Fatalf("admin key leaked into error: %v", err)
	}
}

func TestAnthropicFetch_EmptyKey(t *testing.T) {
	a := NewAnthropic(Options{AdminKey: ""})
	_, err := a.Fetch(context.Background(), Window{})
	if err == nil {
		t.Fatal("expected error for empty admin key")
	}
}

func TestAnthropic_ID(t *testing.T) {
	if got := (&Anthropic{}).ID(); got != "anthropic" {
		t.Errorf("ID() = %q, want anthropic", got)
	}
}
