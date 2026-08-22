package report

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akomyagin/aiCostTracker/internal/provider"
)

func TestAggregateByDay(t *testing.T) {
	w := provider.Window{Start: day(2025, 8, 9), End: day(2025, 8, 14)} // 5 days: 9..13
	records := []provider.UsageRecord{
		{Provider: "openai", Day: day(2025, 8, 10), Model: "gpt-4o", CostMicros: 500_000},
		{Provider: "anthropic", Day: day(2025, 8, 10), Model: "claude", CostMicros: 300_000}, // same day, another provider/model
		{Provider: "openai", Day: day(2025, 8, 12), Model: "gpt-4o", CostMicros: 2_000_000},
		{Provider: "openai", Day: day(2025, 8, 20), Model: "gpt-4o", CostMicros: 9_000_000}, // outside window -> ignored
	}

	days := AggregateByDay(records, w)
	if len(days) != 5 {
		t.Fatalf("got %d days, want 5: %+v", len(days), days)
	}
	// chronological, zero-filled.
	wantCost := []int64{0, 800_000, 0, 2_000_000, 0}
	for i, d := range days {
		if !d.Day.Equal(day(2025, 8, 9+i)) {
			t.Errorf("days[%d].Day = %v", i, d.Day)
		}
		if d.CostMicros != wantCost[i] {
			t.Errorf("days[%d].CostMicros = %d, want %d", i, d.CostMicros, wantCost[i])
		}
	}
}

func TestAggregateByDay_EmptyWindowOrRecords(t *testing.T) {
	w := provider.Window{Start: day(2025, 8, 9), End: day(2025, 8, 12)} // 3 days
	days := AggregateByDay(nil, w)
	if len(days) != 3 {
		t.Fatalf("got %d days, want 3", len(days))
	}
	for i, d := range days {
		if d.CostMicros != 0 {
			t.Errorf("days[%d].CostMicros = %d, want 0", i, d.CostMicros)
		}
	}

	// zero-length window -> empty slice.
	empty := AggregateByDay(nil, provider.Window{Start: day(2025, 8, 9), End: day(2025, 8, 9)})
	if len(empty) != 0 {
		t.Errorf("zero-length window = %v, want empty", empty)
	}
}

func TestBarChart_Scaling(t *testing.T) {
	days := []DayTotal{
		{Day: day(2025, 8, 9), CostMicros: 0},            // zero -> no blocks
		{Day: day(2025, 8, 10), CostMicros: 10_000},      // tiny nonzero (max 100) -> exactly 1 block
		{Day: day(2025, 8, 11), CostMicros: 100_000_000}, // max -> exactly chartWidth blocks
	}

	var buf bytes.Buffer
	if err := BarChart(&buf, days); err != nil {
		t.Fatalf("BarChart: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 { // header + 3 rows
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), buf.String())
	}
	if strings.Count(lines[1], "█") != 0 {
		t.Errorf("zero day should have 0 blocks: %q", lines[1])
	}
	if strings.Count(lines[2], "█") != 1 {
		t.Errorf("tiny day should have 1 block: %q", lines[2])
	}
	if strings.Count(lines[3], "█") != chartWidth {
		t.Errorf("max day should have %d blocks, got %d: %q", chartWidth, strings.Count(lines[3], "█"), lines[3])
	}
}

func TestBarChart_AllZero(t *testing.T) {
	days := []DayTotal{
		{Day: day(2025, 8, 9), CostMicros: 0},
		{Day: day(2025, 8, 10), CostMicros: 0},
	}
	var buf bytes.Buffer
	if err := BarChart(&buf, days); err != nil {
		t.Fatalf("BarChart: %v", err)
	}
	if strings.Contains(buf.String(), "█") {
		t.Errorf("all-zero chart must render no blocks:\n%s", buf.String())
	}
}

func TestBarChart_Golden(t *testing.T) {
	// 7 days: a zero day, the max ($2.00), a minimal nonzero ($0.02), per §4.2.
	days := []DayTotal{
		{Day: day(2025, 8, 9), CostMicros: 0},
		{Day: day(2025, 8, 10), CostMicros: 500_000},
		{Day: day(2025, 8, 11), CostMicros: 2_000_000},
		{Day: day(2025, 8, 12), CostMicros: 0},
		{Day: day(2025, 8, 13), CostMicros: 20_000},
		{Day: day(2025, 8, 14), CostMicros: 1_000_000},
		{Day: day(2025, 8, 15), CostMicros: 0},
	}

	var buf bytes.Buffer
	if err := BarChart(&buf, days); err != nil {
		t.Fatalf("BarChart: %v", err)
	}

	golden := filepath.Join("testdata", "history_chart.golden")
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
		t.Errorf("chart output mismatch.\n--- got ---\n%s\n--- want ---\n%s", buf.String(), want)
	}
}
