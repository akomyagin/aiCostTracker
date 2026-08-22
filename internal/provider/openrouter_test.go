package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// realistic response body shaped after docs/API_NOTES.md §3 / the plan §2:
// two days x two models = 4 rows, each with date, model, total_usage,
// prompt_tokens, completion_tokens.
const openrouterQueryBody = `{
  "data": {
    "data": [
      { "date": "2025-08-02", "model": "anthropic/claude-3.5-sonnet", "total_usage": 0.30, "prompt_tokens": 3000, "completion_tokens": 1500 },
      { "date": "2025-08-01", "model": "openai/gpt-4o",              "total_usage": 0.10, "prompt_tokens": 1000, "completion_tokens": 500 },
      { "date": "2025-08-02", "model": "openai/gpt-4o",              "total_usage": 0.20, "prompt_tokens": 2000, "completion_tokens": 1000 },
      { "date": "2025-08-01", "model": "anthropic/claude-3.5-sonnet", "total_usage": 0.05, "prompt_tokens": 500, "completion_tokens": 250 }
    ],
    "metadata": { "query_time_ms": 12, "row_count": 4, "truncated": false }
  }
}`

var (
	openrouterDay1 = time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	openrouterDay2 = time.Date(2025, 8, 2, 0, 0, 0, 0, time.UTC)
)

func newOpenRouterTestAdapter(t *testing.T, baseURL string, maxRetries int) *OpenRouter {
	t.Helper()
	return NewOpenRouter(Options{
		AdminKey:   "sk-or-mgmt-SECRET",
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		MaxRetries: maxRetries,
	})
}

func TestOpenRouterFetch_NormalizesRows(t *testing.T) {
	start := openrouterDay1
	end := openrouterDay2.AddDate(0, 0, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/analytics/query" {
			t.Errorf("path = %q, want /api/v1/analytics/query", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-or-mgmt-SECRET" {
			t.Errorf("Authorization = %q", got)
		}
		var req openrouterQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if req.Granularity != "day" {
			t.Errorf("granularity = %q, want day", req.Granularity)
		}
		if len(req.Dimensions) != 1 || req.Dimensions[0] != "model" {
			t.Errorf("dimensions = %v, want [model]", req.Dimensions)
		}
		if len(req.Dimensions) > 2 {
			t.Errorf("dimensions length %d exceeds API max of 2", len(req.Dimensions))
		}
		if req.Limit != 1000 {
			t.Errorf("limit = %d, want 1000", req.Limit)
		}
		if want := start.UTC().Format(time.RFC3339); req.TimeRange.Start != want {
			t.Errorf("time_range.start = %q, want %q", req.TimeRange.Start, want)
		}
		if want := end.UTC().Format(time.RFC3339); req.TimeRange.End != want {
			t.Errorf("time_range.end = %q, want %q", req.TimeRange.End, want)
		}
		_, _ = w.Write([]byte(openrouterQueryBody))
	}))
	defer srv.Close()

	o := newOpenRouterTestAdapter(t, srv.URL, 0)
	snap, err := o.Fetch(context.Background(), Window{Start: start, End: end})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if snap.Provider != "openrouter" {
		t.Errorf("Provider = %q, want openrouter", snap.Provider)
	}
	if len(snap.Records) != 4 {
		t.Fatalf("got %d records, want 4: %+v", len(snap.Records), snap.Records)
	}

	// Sorted by (Day, Model).
	want := []UsageRecord{
		{Provider: "openrouter", Day: openrouterDay1, Model: "anthropic/claude-3.5-sonnet", InputTokens: 500, OutputTokens: 250, CostMicros: 50_000},
		{Provider: "openrouter", Day: openrouterDay1, Model: "openai/gpt-4o", InputTokens: 1000, OutputTokens: 500, CostMicros: 100_000},
		{Provider: "openrouter", Day: openrouterDay2, Model: "anthropic/claude-3.5-sonnet", InputTokens: 3000, OutputTokens: 1500, CostMicros: 300_000},
		{Provider: "openrouter", Day: openrouterDay2, Model: "openai/gpt-4o", InputTokens: 2000, OutputTokens: 1000, CostMicros: 200_000},
	}
	for i, wr := range want {
		got := snap.Records[i]
		if !got.Day.Equal(wr.Day) || got.Day.Location() != time.UTC {
			t.Errorf("rec[%d].Day = %v, want %v (UTC midnight)", i, got.Day, wr.Day)
		}
		if got.Model != wr.Model {
			t.Errorf("rec[%d].Model = %q, want %q", i, got.Model, wr.Model)
		}
		if got.InputTokens != wr.InputTokens || got.OutputTokens != wr.OutputTokens {
			t.Errorf("rec[%d] tokens = (%d,%d), want (%d,%d)", i, got.InputTokens, got.OutputTokens, wr.InputTokens, wr.OutputTokens)
		}
		if got.CostMicros != wr.CostMicros {
			t.Errorf("rec[%d].CostMicros = %d, want %d", i, got.CostMicros, wr.CostMicros)
		}
	}
}

func TestOpenRouterFetch_RetriesOn429(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(openrouterQueryBody))
	}))
	defer srv.Close()

	o := newOpenRouterTestAdapter(t, srv.URL, 3)
	o.client.baseDelay = time.Millisecond

	w := Window{Start: openrouterDay1, End: openrouterDay2.AddDate(0, 0, 1)}
	if _, err := o.Fetch(context.Background(), w); err != nil {
		t.Fatalf("Fetch after retry: %v", err)
	}
	if hits != 2 {
		t.Errorf("hit %d times, want 2 (429 then 200)", hits)
	}
}

func TestOpenRouterFetch_FatalForbiddenNoRetry(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "forbidden: inference key, not a management key", http.StatusForbidden)
	}))
	defer srv.Close()

	o := newOpenRouterTestAdapter(t, srv.URL, 3)
	o.client.baseDelay = time.Millisecond

	w := Window{Start: openrouterDay1, End: openrouterDay2.AddDate(0, 0, 1)}
	_, err := o.Fetch(context.Background(), w)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if hits != 1 {
		t.Errorf("hit %d times, want 1 (403 is fatal, not retried)", hits)
	}
	var se *StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusForbidden {
		t.Errorf("want StatusError with 403, got %v", err)
	}
}

func TestOpenRouterFetch_KeyNotLeaked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Response body echoes the key back — harsher than the OpenAI test; this
		// exercises redactSecrets in the retry client.
		http.Error(w, `{"error":"bad request for key sk-or-mgmt-SECRET"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	o := newOpenRouterTestAdapter(t, srv.URL, 0)
	_, err := o.Fetch(context.Background(), Window{Start: openrouterDay1, End: openrouterDay2.AddDate(0, 0, 1)})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "sk-or-mgmt-SECRET") {
		t.Fatalf("management key leaked into error: %v", err)
	}
}

func TestOpenRouterFetch_TruncatedIsError(t *testing.T) {
	const body = `{
  "data": {
    "data": [ { "date": "2025-08-01", "model": "openai/gpt-4o", "total_usage": 0.10, "prompt_tokens": 1000, "completion_tokens": 500 } ],
    "metadata": { "row_count": 1000, "truncated": true }
  }
}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	o := newOpenRouterTestAdapter(t, srv.URL, 0)
	_, err := o.Fetch(context.Background(), Window{Start: openrouterDay1, End: openrouterDay2.AddDate(0, 0, 1)})
	if err == nil {
		t.Fatal("expected error on truncated response")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error should mention truncated: %v", err)
	}
	if strings.Contains(err.Error(), "sk-or-mgmt-SECRET") {
		t.Errorf("key leaked into truncated error: %v", err)
	}
}

func TestOpenRouterFetch_DuplicateRowsAreSummed(t *testing.T) {
	// Two rows for the same (day, model) — the analytics API could plausibly
	// split a group across an extra dimension we don't request. Fetch must sum
	// them rather than overwrite, mirroring the += accumulation in Fetch.
	const body = `{
  "data": {
    "data": [
      { "date": "2025-08-01", "model": "openai/gpt-4o", "total_usage": 0.10, "prompt_tokens": 1000, "completion_tokens": 500 },
      { "date": "2025-08-01", "model": "openai/gpt-4o", "total_usage": 0.02, "prompt_tokens": 200, "completion_tokens": 100 }
    ],
    "metadata": { "row_count": 2, "truncated": false }
  }
}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	o := newOpenRouterTestAdapter(t, srv.URL, 0)
	snap, err := o.Fetch(context.Background(), Window{Start: openrouterDay1, End: openrouterDay2.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Records) != 1 {
		t.Fatalf("got %d records, want 1 (summed): %+v", len(snap.Records), snap.Records)
	}
	got := snap.Records[0]
	// 0.10 + 0.02 accumulates to exactly 120_000 micros (in float64 the sum is
	// 0.12000000000000001) — a drift regression the exact equality would catch.
	if got.CostMicros != 120_000 {
		t.Errorf("CostMicros = %d, want 120_000 ($0.12)", got.CostMicros)
	}
	if got.InputTokens != 1200 || got.OutputTokens != 600 {
		t.Errorf("tokens = (%d,%d), want (1200,600)", got.InputTokens, got.OutputTokens)
	}
}

func TestOpenRouterFetch_EmptyKey(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(openrouterQueryBody))
	}))
	defer srv.Close()

	o := NewOpenRouter(Options{
		AdminKey:   "",
		BaseURL:    srv.URL,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	})
	_, err := o.Fetch(context.Background(), Window{Start: openrouterDay1, End: openrouterDay2.AddDate(0, 0, 1)})
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !strings.Contains(err.Error(), "AICOST_OPENROUTER_ADMIN_KEY") {
		t.Errorf("error should mention env var: %v", err)
	}
	if hits != 0 {
		t.Errorf("server hit %d times, want 0 (empty key must fail before HTTP)", hits)
	}
}

func TestOpenRouter_ID(t *testing.T) {
	if got := (&OpenRouter{}).ID(); got != "openrouter" {
		t.Errorf("ID() = %q, want openrouter", got)
	}
}

func TestOpenRouterRowDay(t *testing.T) {
	want1 := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		row     openrouterRow
		want    time.Time
		wantErr bool
	}{
		{"date bare", openrouterRow{Date: "2025-08-01"}, want1, false},
		{"date rfc3339", openrouterRow{Date: "2025-08-01T13:45:00Z"}, want1, false},
		{"date__day bare", openrouterRow{DateDay: "2025-08-01"}, want1, false},
		{"created_at__day rfc3339", openrouterRow{CreatedDay: "2025-08-01T00:00:00Z"}, want1, false},
		{"all empty", openrouterRow{}, time.Time{}, true},
		{"garbage", openrouterRow{Date: "not-a-date"}, time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.row.day()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tt.want) || got.Location() != time.UTC {
				t.Errorf("day() = %v, want %v (UTC)", got, tt.want)
			}
		})
	}
}
