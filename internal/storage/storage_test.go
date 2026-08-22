package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akomyagin/aiCostTracker/internal/provider"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, for building the legacy schema in tests
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
				provider.UsageRecord{Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude", InputTokens: 100, OutputTokens: 50, CostMicros: 1_500_000},
				provider.UsageRecord{Provider: "openai", Day: day(2025, 8, 2), Model: "gpt-4o", InputTokens: 200, OutputTokens: 80, CostMicros: 2_000_000},
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
			if got[0].CostMicros != 1_500_000 || got[1].CostMicros != 2_000_000 {
				t.Errorf("costs = %d,%d", got[0].CostMicros, got[1].CostMicros)
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
				InputTokens: 100, OutputTokens: 50, CostMicros: 1_000_000,
			})
			if err := s.Save(ctx, first); err != nil {
				t.Fatalf("Save 1: %v", err)
			}

			// Re-fetch of the same period returns updated numbers.
			second := snapshot(provider.UsageRecord{
				Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude",
				InputTokens: 150, OutputTokens: 60, CostMicros: 1_700_000,
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
			if got[0].InputTokens != 150 || got[0].CostMicros != 1_700_000 {
				t.Errorf("row = %+v, want updated values (150 tokens, 1_700_000 micros)", got[0])
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
				provider.UsageRecord{Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude", CostMicros: 1_000_000},
				provider.UsageRecord{Provider: "openai", Day: day(2025, 8, 1), Model: "gpt", CostMicros: 2_000_000},
				provider.UsageRecord{Provider: "anthropic", Day: day(2025, 9, 1), Model: "claude", CostMicros: 3_000_000}, // outside window
			))

			w := provider.Window{Start: day(2025, 8, 1), End: day(2025, 8, 2)}
			got, err := s.Query(ctx, "anthropic", w)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("got %d rows, want 1 (provider+window filter)", len(got))
			}
			if got[0].Provider != "anthropic" || got[0].CostMicros != 1_000_000 {
				t.Errorf("row = %+v", got[0])
			}
		})
	}
}

// TestStore_MicrosRoundTrip stores a precise micro-USD amount and reads it back
// unchanged: integer money survives the SQLite round-trip exactly (a REAL column
// would not for large magnitudes).
func TestStore_MicrosRoundTrip(t *testing.T) {
	for _, f := range factories(t) {
		t.Run(f.name, func(t *testing.T) {
			s := f.make(t)
			ctx := context.Background()
			w := provider.Window{Start: day(2025, 8, 1), End: day(2025, 8, 2)}

			if err := s.Save(ctx, snapshot(provider.UsageRecord{
				Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude", CostMicros: 1_234_567,
			})); err != nil {
				t.Fatalf("Save: %v", err)
			}
			got, err := s.Query(ctx, "", w)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(got) != 1 || got[0].CostMicros != 1_234_567 {
				t.Errorf("round-trip = %+v, want CostMicros 1_234_567", got)
			}
		})
	}
}

// TestOpen_MigratesLegacyRealSchema builds a pre-Этап-9 DB with the old
// `cost_usd REAL` column, then opens it via storage.Open and asserts the history
// is preserved as INTEGER cost_micros (converted via ROUND(cost_usd*1e6)),
// re-opening is idempotent, and the schema no longer carries cost_usd.
func TestOpen_MigratesLegacyRealSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// 1. Create the OLD schema by hand and insert fractional values, including
	//    amounts not exactly representable as float cents.
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	const legacyDDL = `
CREATE TABLE usage_records (
    provider      TEXT    NOT NULL,
    day           TEXT    NOT NULL,
    model         TEXT    NOT NULL,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL    NOT NULL DEFAULT 0,
    fetched_at    TEXT    NOT NULL,
    PRIMARY KEY (provider, day, model)
);`
	if _, err := legacy.Exec(legacyDDL); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	const ins = `INSERT INTO usage_records
        (provider, day, model, input_tokens, output_tokens, cost_usd, fetched_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)`
	fetchedAt := time.Now().UTC().Format(time.RFC3339)
	seed := []struct {
		provider string
		day      string
		model    string
		costUSD  float64
	}{
		{"anthropic", "2025-08-01", "claude", 1.5},
		{"openai", "2025-08-01", "gpt-4o", 0.005},
		{"openrouter", "2025-08-01", "x", 2.0001},
	}
	for _, r := range seed {
		if _, err := legacy.Exec(ins, r.provider, r.day, r.model, 0, 0, r.costUSD, fetchedAt); err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	// 2. Open with the new code (runs migration).
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open (migrate): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// 3. History is on the record, converted to exact micros, count unchanged.
	ctx := context.Background()
	w := provider.Window{Start: day(2025, 8, 1), End: day(2025, 8, 2)}
	got, err := s.Query(ctx, "", w)
	if err != nil {
		t.Fatalf("Query after migrate: %v", err)
	}
	wantMicros := map[string]int64{
		"anthropic":  1_500_000,
		"openai":     5_000,
		"openrouter": 2_000_100,
	}
	if len(got) != len(wantMicros) {
		t.Fatalf("got %d rows after migrate, want %d: %+v", len(got), len(wantMicros), got)
	}
	for _, rec := range got {
		if want := wantMicros[rec.Provider]; rec.CostMicros != want {
			t.Errorf("%s CostMicros = %d, want %d", rec.Provider, rec.CostMicros, want)
		}
	}

	// 4. Re-open the same path: migration is a no-op, data neither doubled nor
	//    distorted.
	if err := s.Close(); err != nil {
		t.Fatalf("close before reopen: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open again (idempotent): %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	got2, err := s2.Query(ctx, "", w)
	if err != nil {
		t.Fatalf("Query after reopen: %v", err)
	}
	if len(got2) != len(wantMicros) {
		t.Fatalf("after reopen got %d rows, want %d (migration not idempotent): %+v", len(got2), len(wantMicros), got2)
	}
	for _, rec := range got2 {
		if want := wantMicros[rec.Provider]; rec.CostMicros != want {
			t.Errorf("after reopen %s CostMicros = %d, want %d", rec.Provider, rec.CostMicros, want)
		}
	}

	// 5. Schema no longer has cost_usd, has cost_micros typed INTEGER.
	inspect, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open for pragma: %v", err)
	}
	defer inspect.Close()
	rows, err := inspect.Query(`PRAGMA table_info(usage_records)`)
	if err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	defer rows.Close()
	var hasLegacy bool
	var microsType string
	var hasMicros bool
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan pragma: %v", err)
		}
		switch name {
		case "cost_usd":
			hasLegacy = true
		case "cost_micros":
			hasMicros = true
			microsType = typ
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pragma: %v", err)
	}
	if hasLegacy {
		t.Error("migrated schema still contains cost_usd column")
	}
	if !hasMicros {
		t.Fatal("migrated schema missing cost_micros column")
	}
	if microsType != "INTEGER" {
		t.Errorf("cost_micros type = %q, want INTEGER", microsType)
	}
}
