package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestJSON_Golden(t *testing.T) {
	rows := []Row{
		{Provider: "anthropic", InputTokens: 250, OutputTokens: 100, CostMicros: 2_500_000},
		{Provider: "openai", InputTokens: 100, OutputTokens: 40, CostMicros: 1_000_000},
	}

	var buf bytes.Buffer
	if err := JSON(&buf, rows); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	golden := filepath.Join("testdata", "report.json.golden")
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
		t.Errorf("json output mismatch.\n--- got ---\n%s\n--- want ---\n%s", buf.String(), want)
	}
}

func TestJSON_EmptyRowsIsEmptyArrayNotNull(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, nil); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	// The byte-level contract: rows must serialize as [], never null.
	if !bytes.Contains(buf.Bytes(), []byte("\"rows\": []")) {
		t.Errorf("empty input should give \"rows\": [], got:\n%s", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("null")) {
		t.Errorf("output must not contain null:\n%s", buf.String())
	}

	// And it must round-trip as valid JSON with a zeroed total. Decode into a
	// local shape that mirrors the wire contract (cost_usd is dollars), not the
	// internal Row (which now carries micro-dollars and no json tags).
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Rows          []struct {
			Provider     string  `json:"provider"`
			InputTokens  int64   `json:"input_tokens"`
			OutputTokens int64   `json:"output_tokens"`
			CostUSD      float64 `json:"cost_usd"`
		} `json:"rows"`
		Total struct {
			InputTokens  int64   `json:"input_tokens"`
			OutputTokens int64   `json:"output_tokens"`
			CostUSD      float64 `json:"cost_usd"`
		} `json:"total"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", doc.SchemaVersion)
	}
	if len(doc.Rows) != 0 {
		t.Errorf("rows = %+v, want empty", doc.Rows)
	}
	if doc.Total.InputTokens != 0 || doc.Total.OutputTokens != 0 || doc.Total.CostUSD != 0 {
		t.Errorf("total = %+v, want zeroed", doc.Total)
	}
}
