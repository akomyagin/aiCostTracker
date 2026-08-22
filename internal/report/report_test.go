package report

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/akomyagin/aiCostTracker/internal/provider"
)

var update = flag.Bool("update", false, "update golden files")

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestAggregate(t *testing.T) {
	records := []provider.UsageRecord{
		{Provider: "openai", Day: day(2025, 8, 1), Model: "gpt-4o", InputTokens: 100, OutputTokens: 40, CostMicros: 1_000_000},
		{Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude", InputTokens: 200, OutputTokens: 90, CostMicros: 2_000_000},
		{Provider: "anthropic", Day: day(2025, 8, 2), Model: "claude", InputTokens: 50, OutputTokens: 10, CostMicros: 500_000},
		{Provider: "", Day: day(2025, 8, 2), Model: "x", CostMicros: 99_000_000}, // empty provider ignored
	}

	rows := Aggregate(records)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// sorted by provider id.
	if rows[0].Provider != "anthropic" || rows[1].Provider != "openai" {
		t.Fatalf("order = %q,%q", rows[0].Provider, rows[1].Provider)
	}
	// anthropic rolls both days together.
	if rows[0].InputTokens != 250 || rows[0].OutputTokens != 100 || rows[0].CostMicros != 2_500_000 {
		t.Errorf("anthropic row = %+v", rows[0])
	}
	if rows[1].CostMicros != 1_000_000 {
		t.Errorf("openai cost = %d", rows[1].CostMicros)
	}
}

func TestAggregate_Empty(t *testing.T) {
	if got := Aggregate(nil); len(got) != 0 {
		t.Errorf("Aggregate(nil) = %v, want empty", got)
	}
}

func TestTable_Golden(t *testing.T) {
	rows := []Row{
		{Provider: "anthropic", InputTokens: 250, OutputTokens: 100, CostMicros: 2_500_000},
		{Provider: "openai", InputTokens: 100, OutputTokens: 40, CostMicros: 1_000_000},
	}

	var buf bytes.Buffer
	if err := Table(&buf, rows); err != nil {
		t.Fatalf("Table: %v", err)
	}

	golden := filepath.Join("testdata", "report_table.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("table output mismatch.\n--- got ---\n%s\n--- want ---\n%s", buf.String(), want)
	}
}

func TestAggregateByModel(t *testing.T) {
	records := []provider.UsageRecord{
		{Provider: "openai", Day: day(2025, 8, 1), Model: "gpt-4o", InputTokens: 100, OutputTokens: 40, CostMicros: 1_000_000},
		{Provider: "openai", Day: day(2025, 8, 2), Model: "gpt-4o", InputTokens: 30, OutputTokens: 10, CostMicros: 300_000}, // same model, another day
		{Provider: "openai", Day: day(2025, 8, 1), Model: "", InputTokens: 10, OutputTokens: 5, CostMicros: 100_000},        // empty model = own bucket
		{Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude", InputTokens: 200, OutputTokens: 90, CostMicros: 2_000_000},
		{Provider: "", Day: day(2025, 8, 2), Model: "x", CostMicros: 99_000_000}, // empty provider ignored
	}

	rows := AggregateByModel(records)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(rows), rows)
	}
	// sorted by (provider, model): anthropic/claude, openai/"", openai/gpt-4o.
	if rows[0].Provider != "anthropic" || rows[0].Model != "claude" {
		t.Errorf("rows[0] = %+v", rows[0])
	}
	if rows[1].Provider != "openai" || rows[1].Model != "" {
		t.Errorf("rows[1] = %+v", rows[1])
	}
	if rows[2].Provider != "openai" || rows[2].Model != "gpt-4o" {
		t.Errorf("rows[2] = %+v", rows[2])
	}
	// gpt-4o rolls both days together.
	if rows[2].InputTokens != 130 || rows[2].OutputTokens != 50 || rows[2].CostMicros != 1_300_000 {
		t.Errorf("gpt-4o row = %+v", rows[2])
	}
}

func TestAggregateByModel_Empty(t *testing.T) {
	if got := AggregateByModel(nil); len(got) != 0 {
		t.Errorf("AggregateByModel(nil) = %v, want empty", got)
	}
}

func TestModelTable_Golden(t *testing.T) {
	rows := []ModelRow{
		{Provider: "anthropic", Model: "claude", InputTokens: 250, OutputTokens: 100, CostMicros: 2_500_000},
		{Provider: "openai", Model: "", InputTokens: 10, OutputTokens: 5, CostMicros: 100_000},
		{Provider: "openai", Model: "gpt-4o", InputTokens: 100, OutputTokens: 40, CostMicros: 1_000_000},
	}

	var buf bytes.Buffer
	if err := ModelTable(&buf, rows); err != nil {
		t.Fatalf("ModelTable: %v", err)
	}

	golden := filepath.Join("testdata", "report_model_table.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("model table output mismatch.\n--- got ---\n%s\n--- want ---\n%s", buf.String(), want)
	}
}

// TestAggregate_NoFloatDrift is the money-precision regression test (Этап 9):
// summing many small equal amounts must be exact. 10_000 records of 10_000
// micros ($0.01) aggregate to exactly 100_000_000 micros ($100.00). In the old
// float64 version, 10_000 * 0.01 accumulated to 100.00000000000335…, which the
// exact equality below would reject. AggregateByModel and AggregateByDay share
// the same += path and are checked with smaller volumes.
func TestAggregate_NoFloatDrift(t *testing.T) {
	t.Run("Aggregate", func(t *testing.T) {
		const n = 10_000
		records := make([]provider.UsageRecord, 0, n)
		for i := 0; i < n; i++ {
			records = append(records, provider.UsageRecord{
				Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude", CostMicros: 10_000,
			})
		}
		rows := Aggregate(records)
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		if rows[0].CostMicros != 100_000_000 {
			t.Errorf("CostMicros = %d, want 100_000_000 ($100.00 exact)", rows[0].CostMicros)
		}
	})

	t.Run("AggregateByModel", func(t *testing.T) {
		const n = 1_000
		records := make([]provider.UsageRecord, 0, n)
		for i := 0; i < n; i++ {
			records = append(records, provider.UsageRecord{
				Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude", CostMicros: 1_000,
			})
		}
		rows := AggregateByModel(records)
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		if rows[0].CostMicros != 1_000_000 {
			t.Errorf("CostMicros = %d, want 1_000_000 ($1.00 exact)", rows[0].CostMicros)
		}
	})

	t.Run("AggregateByDay", func(t *testing.T) {
		const n = 1_000
		w := provider.Window{Start: day(2025, 8, 1), End: day(2025, 8, 2)}
		records := make([]provider.UsageRecord, 0, n)
		for i := 0; i < n; i++ {
			records = append(records, provider.UsageRecord{
				Provider: "anthropic", Day: day(2025, 8, 1), Model: "claude", CostMicros: 1_000,
			})
		}
		days := AggregateByDay(records, w)
		if len(days) != 1 {
			t.Fatalf("got %d days, want 1", len(days))
		}
		if days[0].CostMicros != 1_000_000 {
			t.Errorf("CostMicros = %d, want 1_000_000 ($1.00 exact)", days[0].CostMicros)
		}
	})
}

func TestTable_EmptyRowsHasTotal(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, nil); err != nil {
		t.Fatalf("Table: %v", err)
	}
	// Even with no rows, the header and a zeroed TOTAL line must render.
	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("TOTAL")) {
		t.Errorf("empty table missing TOTAL footer:\n%s", out)
	}
}
