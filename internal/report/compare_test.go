package report

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareTables_Delta(t *testing.T) {
	tests := []struct {
		name     string
		cur      []Row
		prev     []Row
		wantLine string
	}{
		{
			name:     "growth",
			cur:      []Row{{Provider: "anthropic", CostMicros: 3_500_000}},
			prev:     []Row{{Provider: "anthropic", CostMicros: 2_000_000}},
			wantLine: "TOTAL: $3.50 vs $2.00 (+$1.50, +75.0%)",
		},
		{
			name:     "decline",
			cur:      []Row{{Provider: "anthropic", CostMicros: 1_000_000}},
			prev:     []Row{{Provider: "anthropic", CostMicros: 4_000_000}},
			wantLine: "TOTAL: $1.00 vs $4.00 (-$3.00, -75.0%)",
		},
		{
			name:     "equal",
			cur:      []Row{{Provider: "anthropic", CostMicros: 2_000_000}},
			prev:     []Row{{Provider: "anthropic", CostMicros: 2_000_000}},
			wantLine: "TOTAL: $2.00 vs $2.00 (+$0.00, +0.0%)",
		},
		{
			name:     "empty previous",
			cur:      []Row{{Provider: "anthropic", CostMicros: 1_500_000}},
			prev:     nil,
			wantLine: "TOTAL: $1.50 vs $0.00 (+$1.50, n/a)",
		},
		{
			// A sub-cent delta (-100 micros = -$0.0001) must snap to zero and not
			// print as "-$0.00, -0.0%" — the negative-zero artifact this guard
			// still prevents even though the old float rounding residue is gone.
			name:     "negligible negative delta rounds to positive zero",
			cur:      []Row{{Provider: "anthropic", CostMicros: 2_000_000}},
			prev:     []Row{{Provider: "anthropic", CostMicros: 2_000_100}},
			wantLine: "TOTAL: $2.00 vs $2.00 (+$0.00, +0.0%)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := CompareTables(&buf, "cur", "prev", tt.cur, tt.prev); err != nil {
				t.Fatalf("CompareTables: %v", err)
			}
			if !strings.Contains(buf.String(), tt.wantLine) {
				t.Errorf("missing %q in:\n%s", tt.wantLine, buf.String())
			}
		})
	}
}

func TestCompareTables_Golden(t *testing.T) {
	cur := []Row{
		{Provider: "anthropic", InputTokens: 250, OutputTokens: 100, CostMicros: 2_500_000},
		{Provider: "openai", InputTokens: 100, OutputTokens: 40, CostMicros: 1_000_000},
	}
	prev := []Row{
		{Provider: "anthropic", InputTokens: 200, OutputTokens: 80, CostMicros: 2_000_000},
	}

	var buf bytes.Buffer
	if err := CompareTables(&buf, "2025-08-01..2025-08-15", "2025-07-01..2025-07-31", cur, prev); err != nil {
		t.Fatalf("CompareTables: %v", err)
	}

	golden := filepath.Join("testdata", "history_compare.golden")
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
		t.Errorf("compare output mismatch.\n--- got ---\n%s\n--- want ---\n%s", buf.String(), want)
	}
}
