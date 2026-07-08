package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// realistic response bodies copied from docs/API_NOTES.md §2 (quoting the live
// OpenAI Usage/Costs API docs).

const openaiCostBody = `{
  "object": "page",
  "data": [
    {
      "object": "bucket",
      "start_time": 1754006400,
      "end_time": 1754092800,
      "results": [
        {
          "object": "organization.costs.result",
          "amount": { "value": 0.06, "currency": "usd" },
          "line_item": "gpt-4o-2024-08-06, input",
          "project_id": "proj_01"
        },
        {
          "object": "organization.costs.result",
          "amount": { "value": 0.12, "currency": "usd" },
          "line_item": "gpt-4o-2024-08-06, output",
          "project_id": "proj_01"
        }
      ]
    }
  ],
  "has_more": false,
  "next_page": null
}`

const openaiUsageBody = `{
  "object": "page",
  "data": [
    {
      "object": "bucket",
      "start_time": 1754006400,
      "end_time": 1754092800,
      "results": [
        {
          "object": "organization.usage.completions.result",
          "input_tokens": 1000,
          "output_tokens": 500,
          "input_cached_tokens": 200,
          "num_model_requests": 5,
          "model": "gpt-4o-2024-08-06"
        }
      ]
    }
  ],
  "has_more": false,
  "next_page": null
}`

// 1754006400 == 2025-08-01T00:00:00Z.
var openaiWantDay = time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)

func newOpenAITestAdapter(t *testing.T, baseURL string, maxRetries int) *OpenAI {
	t.Helper()
	return NewOpenAI(Options{
		AdminKey:   "sk-admin-SECRET",
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		MaxRetries: maxRetries,
	})
}

func TestOpenAIFetch_MergesCostAndTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/organization/costs"):
			// verify start_time is Unix seconds, not RFC3339
			if got := r.URL.Query().Get("start_time"); got != "1754006400" {
				t.Errorf("costs start_time = %q, want 1754006400 (unix)", got)
			}
			_, _ = w.Write([]byte(openaiCostBody))
		case strings.Contains(r.URL.Path, "/organization/usage/completions"):
			_, _ = w.Write([]byte(openaiUsageBody))
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	o := newOpenAITestAdapter(t, srv.URL, 0)
	w := Window{Start: openaiWantDay, End: openaiWantDay.AddDate(0, 0, 1)}

	snap, err := o.Fetch(context.Background(), w)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Records) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(snap.Records), snap.Records)
	}
	rec := snap.Records[0]
	if !rec.Day.Equal(openaiWantDay) {
		t.Errorf("Day = %v, want %v", rec.Day, openaiWantDay)
	}
	if rec.Model != "gpt-4o-2024-08-06" {
		t.Errorf("Model = %q", rec.Model)
	}
	if rec.InputTokens != 1000 || rec.OutputTokens != 500 {
		t.Errorf("tokens = (%d,%d), want (1000,500)", rec.InputTokens, rec.OutputTokens)
	}
	if rec.CostUSD < 0.17 || rec.CostUSD > 0.19 {
		t.Errorf("CostUSD = %v, want ~0.18", rec.CostUSD)
	}
}

func TestOpenAIFetch_RetriesOn5xx(t *testing.T) {
	var costHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/organization/costs"):
			costHits++
			if costHits == 1 {
				http.Error(w, "boom", http.StatusServiceUnavailable) // 503 retryable
				return
			}
			_, _ = w.Write([]byte(openaiCostBody))
		case strings.Contains(r.URL.Path, "/organization/usage/completions"):
			_, _ = w.Write([]byte(openaiUsageBody))
		}
	}))
	defer srv.Close()

	o := newOpenAITestAdapter(t, srv.URL, 3)
	o.client.baseDelay = time.Millisecond

	w := Window{Start: openaiWantDay, End: openaiWantDay.AddDate(0, 0, 1)}
	if _, err := o.Fetch(context.Background(), w); err != nil {
		t.Fatalf("Fetch after retry: %v", err)
	}
	if costHits != 2 {
		t.Errorf("cost hit %d times, want 2 (503 then 200)", costHits)
	}
}

func TestOpenAIFetch_KeyNotLeaked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	o := newOpenAITestAdapter(t, srv.URL, 0)
	_, err := o.Fetch(context.Background(), Window{Start: openaiWantDay, End: openaiWantDay.AddDate(0, 0, 1)})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "sk-admin-SECRET") {
		t.Fatalf("admin key leaked into error: %v", err)
	}
}

func TestModelFromLineItem(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"gpt-4o-2024-08-06, input", "gpt-4o-2024-08-06"},
		{"gpt-4o-2024-08-06, output", "gpt-4o-2024-08-06"},
		{"o1-mini", "o1-mini"},
		{"", ""},
		{"  gpt-4 , cached ", "gpt-4"},
	}
	for _, tt := range tests {
		if got := modelFromLineItem(tt.in); got != tt.want {
			t.Errorf("modelFromLineItem(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOpenAI_ID(t *testing.T) {
	if got := (&OpenAI{}).ID(); got != "openai" {
		t.Errorf("ID() = %q, want openai", got)
	}
}
