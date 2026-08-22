package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akomyagin/aiCostTracker/internal/provider"
)

// storeFactory lets the shared test body run against both the SQLite store and
// the in-memory fake, ensuring they agree on semantics.
type storeFactory struct {
	name string
	make func(t *testing.T) Store
}

func factories(t *testing.T) []storeFactory {
	return []storeFactory{
		{
			name: "sqlite",
			make: func(t *testing.T) Store {
				path := filepath.Join(t.TempDir(), "test.db")
				s, err := Open(path)
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				t.Cleanup(func() { _ = s.Close() })
				return s
			},
		},
		{
			name: "fake",
			make: func(t *testing.T) Store { return NewFake() },
		},
	}
}

func TestOpen_CreatesMissingParentDir(t *testing.T) {
	// A fresh system has no ~/.config/aicost/ yet; Open must create the whole
	// parent chain rather than requiring it to pre-exist.
	path := filepath.Join(t.TempDir(), "nested", "does", "not", "exist", "history.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open with missing parent dirs: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("db file not created at %s: %v", path, err)
	}
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func snapshot(records ...provider.UsageRecord) provider.Snapshot {
	return provider.Snapshot{FetchedAt: time.Now().UTC(), Records: records}
}

func TestStore_SaveAndQuery(t *testing.T) {
	for _, f := range factories(t) {
		t.Run(f.name, func(t *testing.T) {
			s := f.make(t)
			ctx := context.Background()

			snap := snapshot(
				provider.UsageRecord{Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude", InputTokens: 100, OutputTokens: 50, CostUSD: 1.5},
				provider.UsageRecord{Provider: "openai", Day: day(2025, 8, 2), Model: "gpt-4o", InputTokens: 200, OutputTokens: 80, CostUSD: 2.0},
			)
			if err := s.Save(ctx, snap); err != nil {
				t.Fatalf("Save: %v", err)
			}

			w := provider.Window{Start: day(2025, 8, 1), End: day(2025, 8, 3)}
			got, err := s.Query(ctx, "", w)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(got) != 2 {
				t.Fatalf("got %d rows, want 2", len(got))
			}
			// sorted by (day, provider, model): 8-1 anthropic, then 8-2 openai.
			if got[0].Provider != "anthropic" || got[1].Provider != "openai" {
				t.Errorf("order = %q,%q", got[0].Provider, got[1].Provider)
			}
			if got[0].CostUSD != 1.5 || got[1].CostUSD != 2.0 {
				t.Errorf("costs = %v,%v", got[0].CostUSD, got[1].CostUSD)
			}
		})
	}
}

// TestStore_IdempotentUpsert is the required test: saving the same (provider,
// day, model) twice must not double-count and must reflect the latest values.
func TestStore_IdempotentUpsert(t *testing.T) {
	for _, f := range factories(t) {
		t.Run(f.name, func(t *testing.T) {
			s := f.make(t)
			ctx := context.Background()
			w := provider.Window{Start: day(2025, 8, 1), End: day(2025, 8, 2)}

			first := snapshot(provider.UsageRecord{
				Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude",
				InputTokens: 100, OutputTokens: 50, CostUSD: 1.0,
			})
			if err := s.Save(ctx, first); err != nil {
				t.Fatalf("Save 1: %v", err)
			}

			// Re-fetch of the same period returns updated numbers.
			second := snapshot(provider.UsageRecord{
				Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude",
				InputTokens: 150, OutputTokens: 60, CostUSD: 1.7,
			})
			if err := s.Save(ctx, second); err != nil {
				t.Fatalf("Save 2: %v", err)
			}

			got, err := s.Query(ctx, "", w)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("got %d rows, want 1 (upsert must not duplicate)", len(got))
			}
			if got[0].InputTokens != 150 || got[0].CostUSD != 1.7 {
				t.Errorf("row = %+v, want updated values (150 tokens, 1.7 usd)", got[0])
			}
		})
	}
}

func TestStore_QueryFiltersByProviderAndWindow(t *testing.T) {
	for _, f := range factories(t) {
		t.Run(f.name, func(t *testing.T) {
			s := f.make(t)
			ctx := context.Background()
			_ = s.Save(ctx, snapshot(
				provider.UsageRecord{Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude", CostUSD: 1},
				provider.UsageRecord{Provider: "openai", Day: day(2025, 8, 1), Model: "gpt", CostUSD: 2},
				provider.UsageRecord{Provider: "anthropic", Day: day(2025, 9, 1), Model: "claude", CostUSD: 3}, // outside window
			))

			w := provider.Window{Start: day(2025, 8, 1), End: day(2025, 8, 2)}
			got, err := s.Query(ctx, "anthropic", w)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("got %d rows, want 1 (provider+window filter)", len(got))
			}
			if got[0].Provider != "anthropic" || got[0].CostUSD != 1 {
				t.Errorf("row = %+v", got[0])
			}
		})
	}
}
