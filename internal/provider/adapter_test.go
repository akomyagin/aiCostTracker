package provider

import (
	"testing"
	"time"
)

func TestMergeCostsAndTokens_UnionAndSort(t *testing.T) {
	d1 := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2025, 8, 2, 0, 0, 0, 0, time.UTC)

	costs := map[dayModel]int64{
		{day: d2, model: "b"}: 2_000_000,
		{day: d1, model: "a"}: 1_000_000,
		{day: d1, model: "z"}: 500_000, // cost-only key (no tokens)
	}
	tokens := map[dayModel]tokenCounts{
		{day: d1, model: "a"}: {input: 10, output: 5},
		{day: d2, model: "b"}: {input: 20, output: 7},
		{day: d2, model: "y"}: {input: 3, output: 1}, // token-only key (no cost)
	}

	// Union of keys: {d1/a, d1/z, d2/b, d2/y} = 4 distinct records.
	got := mergeCostsAndTokens("prov", costs, tokens)
	if len(got) != 4 {
		t.Fatalf("got %d records, want 4", len(got))
	}

	// Must be sorted by (day, model): d1/a, d1/z, d2/b, d2/y.
	wantOrder := []struct {
		day   time.Time
		model string
	}{
		{d1, "a"}, {d1, "z"}, {d2, "b"}, {d2, "y"},
	}
	for i, w := range wantOrder {
		if !got[i].Day.Equal(w.day) || got[i].Model != w.model {
			t.Errorf("record %d = (%v,%q), want (%v,%q)", i, got[i].Day, got[i].Model, w.day, w.model)
		}
	}

	// d1/a has both cost and tokens.
	if got[0].CostMicros != 1_000_000 || got[0].InputTokens != 10 || got[0].OutputTokens != 5 {
		t.Errorf("d1/a = %+v", got[0])
	}
	// d1/z is cost-only.
	if got[1].CostMicros != 500_000 || got[1].InputTokens != 0 {
		t.Errorf("d1/z cost-only = %+v", got[1])
	}
	// d2/y (index 3) is token-only.
	if got[3].CostMicros != 0 || got[3].InputTokens != 3 {
		t.Errorf("d2/y token-only = %+v", got[3])
	}
}

func TestParseUTCDay(t *testing.T) {
	got, err := parseUTCDay("2025-08-01T13:45:00Z")
	if err != nil {
		t.Fatalf("parseUTCDay: %v", err)
	}
	want := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}

	if _, err := parseUTCDay("not-a-date"); err == nil {
		t.Error("expected error for invalid timestamp")
	}
}

func TestUnixToUTCDay(t *testing.T) {
	// 1754055900 == 2025-08-01T13:45:00Z
	got := unixToUTCDay(1754055900)
	want := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOrDefault(t *testing.T) {
	if orDefault("", "def") != "def" {
		t.Error("empty should fall back to default")
	}
	if orDefault("x", "def") != "x" {
		t.Error("non-empty should win")
	}
}
