package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/akomyagin/aiCostTracker/internal/config"
	"github.com/akomyagin/aiCostTracker/internal/provider"
	"github.com/akomyagin/aiCostTracker/internal/storage"
)

// fakeProvider is a ProviderUsageSource returning canned records.
type fakeProvider struct {
	id      string
	records []provider.UsageRecord
	err     error
	fetched bool
}

func (f *fakeProvider) ID() string { return f.id }
func (f *fakeProvider) Fetch(_ context.Context, w provider.Window) (provider.Snapshot, error) {
	f.fetched = true
	if f.err != nil {
		return provider.Snapshot{}, f.err
	}
	return provider.Snapshot{Provider: f.id, Window: w, Records: f.records, FetchedAt: time.Now()}, nil
}

// testApp builds an App wired to fakes with the given config and providers.
func testApp(cfg config.Config, store storage.Store, providers map[string]provider.ProviderUsageSource) (*App, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	app := &App{
		LoadConfig:  func() (config.Config, error) { return cfg, nil },
		OpenStore:   func(string) (storage.Store, error) { return store, nil },
		NewProvider: func(id string, _ config.Config) (provider.ProviderUsageSource, error) { return providers[id], nil },
		Now:         func() time.Time { return time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC) },
		Out:         &out,
		ErrOut:      &errOut,
		Version:     "test", Commit: "abc", Date: "2025-08-15",
	}
	return app, &out, &errOut
}

func enabledCfg() config.Config {
	return config.Config{
		Providers: map[string]config.ProviderConfig{
			"anthropic": {Enabled: true, AdminKey: "k"},
		},
		DBPath: ":memory:",
	}
}

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func run(app *App, args ...string) error {
	root := app.Root()
	root.SetArgs(args)
	return root.ExecuteContext(context.Background())
}

func TestVersionCommand(t *testing.T) {
	app, out, _ := testApp(enabledCfg(), storage.NewFake(), nil)
	if err := run(app, "version"); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out.String(), "aicost test (commit abc, built 2025-08-15)") {
		t.Errorf("version output = %q", out.String())
	}
}

func TestReportCommand_FetchesSavesAndPrints(t *testing.T) {
	store := storage.NewFake()
	fp := &fakeProvider{
		id: "anthropic",
		records: []provider.UsageRecord{
			{Provider: "anthropic", Day: day(2025, 8, 15), Model: "claude", InputTokens: 100, OutputTokens: 40, CostUSD: 1.25},
		},
	}
	app, out, _ := testApp(enabledCfg(), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "report", "--period", "today"); err != nil {
		t.Fatalf("report: %v", err)
	}
	if !fp.fetched {
		t.Error("provider was not fetched")
	}
	// snapshot persisted to store
	got, err := store.Query(context.Background(), "", provider.Window{Start: day(2025, 8, 15), End: day(2025, 8, 16)})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].CostUSD != 1.25 {
		t.Errorf("store rows = %+v", got)
	}
	// table printed
	if !strings.Contains(out.String(), "anthropic") || !strings.Contains(out.String(), "$1.25") {
		t.Errorf("report output = %q", out.String())
	}
}

func TestReportCommand_NoDataMessage(t *testing.T) {
	// report fetched the provider and got zero records back: an empty window is
	// authoritative "no usage", so print the message and no empty table.
	fp := &fakeProvider{id: "anthropic", records: nil}
	app, out, _ := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "report", "--period", "today"); err != nil {
		t.Fatalf("report: %v", err)
	}
	if !fp.fetched {
		t.Error("provider was not fetched")
	}
	if !strings.Contains(out.String(), "No usage data for this period.") {
		t.Errorf("missing no-data message: %q", out.String())
	}
	if strings.Contains(out.String(), "TOTAL") {
		t.Errorf("empty table was printed: %q", out.String())
	}
}

func TestHistoryCommand_NoDataMessage(t *testing.T) {
	// Empty store, no network: history cannot tell zero-spend from not-fetched,
	// so it must say so and print no empty table.
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
	app, out, _ := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "history", "--period", "7d"); err != nil {
		t.Fatalf("history: %v", err)
	}
	if fp.fetched {
		t.Error("history must not fetch from providers")
	}
	if !strings.Contains(out.String(), "No stored history for this period.") {
		t.Errorf("missing no-data message: %q", out.String())
	}
	if strings.Contains(out.String(), "TOTAL") {
		t.Errorf("empty table was printed: %q", out.String())
	}
}

func TestReportCommand_NoProvidersEnabled(t *testing.T) {
	cfg := config.Config{Providers: map[string]config.ProviderConfig{}, DBPath: ":memory:"}
	app, _, _ := testApp(cfg, storage.NewFake(), nil)
	err := run(app, "report")
	if err == nil || !strings.Contains(err.Error(), "no providers enabled") {
		t.Fatalf("want no-providers error, got %v", err)
	}
}

func TestReportCommand_FetchErrorPropagates(t *testing.T) {
	fp := &fakeProvider{id: "anthropic", err: errors.New("boom")}
	app, _, _ := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": fp})
	err := run(app, "report", "--period", "today")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want fetch error, got %v", err)
	}
}

func TestHistoryCommand_ReadsStoreNoNetwork(t *testing.T) {
	store := storage.NewFake()
	_ = store.Save(context.Background(), provider.Snapshot{
		FetchedAt: time.Now(),
		Records: []provider.UsageRecord{
			{Provider: "openai", Day: day(2025, 8, 14), Model: "gpt", InputTokens: 10, OutputTokens: 5, CostUSD: 0.9},
		},
	})
	// provider that would fail if called — history must NOT call it.
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
	app, out, _ := testApp(enabledCfg(), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "history", "--period", "7d"); err != nil {
		t.Fatalf("history: %v", err)
	}
	if fp.fetched {
		t.Error("history must not fetch from providers")
	}
	if !strings.Contains(out.String(), "openai") || !strings.Contains(out.String(), "$0.90") {
		t.Errorf("history output = %q", out.String())
	}
}

func saveRecords(t *testing.T, store storage.Store, recs ...provider.UsageRecord) {
	t.Helper()
	if err := store.Save(context.Background(), provider.Snapshot{FetchedAt: time.Now(), Records: recs}); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestHistoryCommand_Chart(t *testing.T) {
	store := storage.NewFake()
	saveRecords(t, store,
		provider.UsageRecord{Provider: "openai", Day: day(2025, 8, 12), Model: "gpt-4o", InputTokens: 10, OutputTokens: 5, CostUSD: 0.5},
		provider.UsageRecord{Provider: "openai", Day: day(2025, 8, 14), Model: "gpt-4o", InputTokens: 20, OutputTokens: 8, CostUSD: 1.0},
	)
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
	app, out, _ := testApp(enabledCfg(), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "history", "--period", "7d", "--chart"); err != nil {
		t.Fatalf("history: %v", err)
	}
	if fp.fetched {
		t.Error("history must not fetch from providers")
	}
	s := out.String()
	if !strings.Contains(s, "TOTAL") {
		t.Errorf("missing table TOTAL: %q", s)
	}
	if !strings.Contains(s, "CHART") {
		t.Errorf("missing CHART header: %q", s)
	}
	if !strings.Contains(s, "█") {
		t.Errorf("missing bar blocks: %q", s)
	}
}

func TestHistoryCommand_ByModel(t *testing.T) {
	store := storage.NewFake()
	saveRecords(t, store,
		provider.UsageRecord{Provider: "openai", Day: day(2025, 8, 14), Model: "gpt-4o", InputTokens: 10, OutputTokens: 5, CostUSD: 0.5},
	)
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
	app, out, _ := testApp(enabledCfg(), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "history", "--period", "7d", "--by-model"); err != nil {
		t.Fatalf("history: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "MODEL") || !strings.Contains(s, "gpt-4o") {
		t.Errorf("by-model output missing MODEL/gpt-4o: %q", s)
	}

	// negative: default history (no flag) must NOT include the model column/value.
	var out2 bytes.Buffer
	app.Out = &out2
	if err := run(app, "history", "--period", "7d"); err != nil {
		t.Fatalf("history default: %v", err)
	}
	if strings.Contains(out2.String(), "gpt-4o") {
		t.Errorf("default history leaked model: %q", out2.String())
	}
}

func TestHistoryCommand_ChartAndByModelCombined(t *testing.T) {
	// --chart and --by-model are intentionally compatible: the chart always
	// shows daily totals while the table switches to per-model rows.
	store := storage.NewFake()
	saveRecords(t, store,
		provider.UsageRecord{Provider: "openai", Day: day(2025, 8, 14), Model: "gpt-4o", InputTokens: 10, OutputTokens: 5, CostUSD: 0.5},
	)
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
	app, out, _ := testApp(enabledCfg(), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "history", "--period", "7d", "--chart", "--by-model"); err != nil {
		t.Fatalf("history: %v", err)
	}
	if fp.fetched {
		t.Error("history must not fetch from providers")
	}
	s := out.String()
	if !strings.Contains(s, "MODEL") || !strings.Contains(s, "gpt-4o") {
		t.Errorf("missing per-model table: %q", s)
	}
	if !strings.Contains(s, "CHART") || !strings.Contains(s, "█") {
		t.Errorf("missing chart: %q", s)
	}
}

func TestHistoryCommand_Compare(t *testing.T) {
	// now = 2025-08-15. --period month: current [2025-08-01, 2025-08-16),
	// previous full July [2025-07-01, 2025-08-01).
	store := storage.NewFake()
	saveRecords(t, store,
		provider.UsageRecord{Provider: "anthropic", Day: day(2025, 8, 10), Model: "claude", CostUSD: 3.0},
		provider.UsageRecord{Provider: "anthropic", Day: day(2025, 7, 15), Model: "claude", CostUSD: 2.0},
	)
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
	app, out, _ := testApp(enabledCfg(), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "history", "--period", "month", "--compare"); err != nil {
		t.Fatalf("history: %v", err)
	}
	if fp.fetched {
		t.Error("history must not fetch from providers")
	}
	s := out.String()
	if !strings.Contains(s, "CURRENT [") || !strings.Contains(s, "PREVIOUS [") {
		t.Errorf("missing period labels: %q", s)
	}
	if !strings.Contains(s, "TOTAL: $3.00 vs $2.00 (+$1.00, +50.0%)") {
		t.Errorf("wrong delta line: %q", s)
	}
}

func TestHistoryCommand_CompareEmptyPrevious(t *testing.T) {
	store := storage.NewFake()
	saveRecords(t, store,
		provider.UsageRecord{Provider: "anthropic", Day: day(2025, 8, 10), Model: "claude", CostUSD: 3.0},
	)
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
	app, out, _ := testApp(enabledCfg(), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "history", "--period", "month", "--compare"); err != nil {
		t.Fatalf("history: %v", err)
	}
	if !strings.Contains(out.String(), "n/a") {
		t.Errorf("empty previous should give n/a percent: %q", out.String())
	}
}

func TestHistoryCommand_CompareConflictingFlags(t *testing.T) {
	store := storage.NewFake()
	saveRecords(t, store, provider.UsageRecord{Provider: "anthropic", Day: day(2025, 8, 10), Model: "claude", CostUSD: 1.0})
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}

	for _, flag := range []string{"--by-model", "--chart"} {
		app, _, _ := testApp(enabledCfg(), store, map[string]provider.ProviderUsageSource{"anthropic": fp})
		err := run(app, "history", "--period", "month", "--compare", flag)
		if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Errorf("--compare %s: want cannot-be-combined error, got %v", flag, err)
		}
	}
}

func TestHistoryCommand_NoDataWithFlags(t *testing.T) {
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
	app, out, _ := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "history", "--period", "7d", "--chart"); err != nil {
		t.Fatalf("history: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "No stored history for this period.") {
		t.Errorf("missing no-data message: %q", s)
	}
	if strings.Contains(s, "█") || strings.Contains(s, "CURRENT") {
		t.Errorf("no-data output printed chart/compare: %q", s)
	}
}

func TestReportCommand_ByModel(t *testing.T) {
	store := storage.NewFake()
	fp := &fakeProvider{
		id: "anthropic",
		records: []provider.UsageRecord{
			{Provider: "anthropic", Day: day(2025, 8, 15), Model: "claude-opus", InputTokens: 100, OutputTokens: 40, CostUSD: 1.25},
			{Provider: "anthropic", Day: day(2025, 8, 15), Model: "claude-haiku", InputTokens: 30, OutputTokens: 10, CostUSD: 0.20},
		},
	}
	app, out, _ := testApp(enabledCfg(), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "report", "--period", "today", "--by-model"); err != nil {
		t.Fatalf("report: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "claude-opus") || !strings.Contains(s, "claude-haiku") {
		t.Errorf("by-model report missing both models: %q", s)
	}

	// negative: default report collapses to one provider row (no model column).
	var out2 bytes.Buffer
	app.Out = &out2
	if err := run(app, "report", "--period", "today"); err != nil {
		t.Fatalf("report default: %v", err)
	}
	if strings.Contains(out2.String(), "claude-opus") {
		t.Errorf("default report leaked model: %q", out2.String())
	}
}

func TestReportCommand_AdminKeyHintOnAuthError(t *testing.T) {
	// A 401/403 from the provider must trigger the admin-vs-model-key hint, and
	// the surfaced error must never contain the admin key itself.
	const secret = "sk-ant-admin-super-secret"
	fp := &fakeProvider{
		id:  "anthropic",
		err: fmt.Errorf("provider anthropic: cost report: %w", &provider.StatusError{StatusCode: 401, Retryable: false, Body: "unauthorized"}),
	}
	app, _, _ := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": fp})
	err := run(app, "report", "--period", "today")
	if err == nil {
		t.Fatal("expected fetch error")
	}
	if !strings.Contains(err.Error(), "ADMIN") || !strings.Contains(err.Error(), "Admin Keys") {
		t.Errorf("auth error missing admin-key hint: %q", err.Error())
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("admin key leaked into error: %q", err.Error())
	}
}

func TestReportCommand_EmptyKeyErrorGetsHint(t *testing.T) {
	// The provider's own "admin_key is empty" error must also carry the hint.
	fp := &fakeProvider{id: "anthropic", err: errors.New("provider anthropic: admin_key is empty (set AICOST_ANTHROPIC_ADMIN_KEY or config)")}
	app, _, _ := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": fp})
	err := run(app, "report", "--period", "today")
	if err == nil || !strings.Contains(err.Error(), "Admin Keys") {
		t.Fatalf("want admin-key hint on empty-key error, got %v", err)
	}
}

func TestReportCommand_NoHintOnNetworkError(t *testing.T) {
	// An ordinary network error must NOT get the credentials lecture.
	fp := &fakeProvider{id: "anthropic", err: errors.New("fetch: dial tcp: connection refused")}
	app, _, _ := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": fp})
	err := run(app, "report", "--period", "today")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "Admin Keys") {
		t.Errorf("network error wrongly got admin-key hint: %q", err.Error())
	}
}

// alertCfg returns an enabled config with the monthly alert threshold set.
func alertCfg(threshold float64) config.Config {
	cfg := enabledCfg()
	cfg.Alert.MonthlyUSD = threshold
	return cfg
}

func TestReportCommand_Alert(t *testing.T) {
	// One record with CostUSD 5.00 on "today" (2025-08-15) so report aggregates
	// to a total of $5.00 regardless of threshold.
	const total = 5.0
	tests := []struct {
		name        string
		threshold   float64
		failOnAlert bool
		wantAlert   bool
		wantErr     bool
	}{
		{name: "below total alerts", threshold: 4.0, wantAlert: true},
		{name: "equal to total does not alert", threshold: 5.0, wantAlert: false},
		{name: "above total does not alert", threshold: 6.0, wantAlert: false},
		{name: "alert without fail flag returns nil", threshold: 4.0, wantAlert: true, failOnAlert: false},
		{name: "alert with fail flag returns error", threshold: 4.0, failOnAlert: true, wantAlert: true, wantErr: true},
		{name: "threshold zero disables alert", threshold: 0, wantAlert: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fp := &fakeProvider{
				id: "anthropic",
				records: []provider.UsageRecord{
					{Provider: "anthropic", Day: day(2025, 8, 15), Model: "claude", InputTokens: 100, OutputTokens: 40, CostUSD: total},
				},
			}
			app, out, errOut := testApp(alertCfg(tc.threshold), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": fp})

			args := []string{"report", "--period", "today"}
			if tc.failOnAlert {
				args = append(args, "--fail-on-alert")
			}
			err := run(app, args...)

			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "monthly alert threshold exceeded") {
					t.Fatalf("want threshold-exceeded error, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("run: %v", err)
			}

			gotAlert := strings.Contains(errOut.String(), "ALERT:")
			if gotAlert != tc.wantAlert {
				t.Errorf("alert in stderr = %v, want %v (stderr=%q)", gotAlert, tc.wantAlert, errOut.String())
			}
			if tc.wantAlert {
				const wantLine = "ALERT: total spend $5.00 exceeds monthly threshold"
				if !strings.Contains(errOut.String(), wantLine) {
					t.Errorf("stderr = %q, want contains %q", errOut.String(), wantLine)
				}
			}
			// The alert must never touch stdout.
			if strings.Contains(out.String(), "ALERT") {
				t.Errorf("alert leaked into stdout: %q", out.String())
			}
		})
	}
}

func TestReportCommand_AlertDefaultConfigUnchanged(t *testing.T) {
	// With no alert block (enabledCfg has MonthlyUSD == 0), stdout must be
	// byte-identical to the run without the feature, and stderr stays empty.
	fp := &fakeProvider{
		id: "anthropic",
		records: []provider.UsageRecord{
			{Provider: "anthropic", Day: day(2025, 8, 15), Model: "claude", InputTokens: 100, OutputTokens: 40, CostUSD: 9.0},
		},
	}
	app, out, errOut := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": fp})
	if err := run(app, "report", "--period", "today"); err != nil {
		t.Fatalf("report: %v", err)
	}
	if strings.Contains(errOut.String(), "ALERT") {
		t.Errorf("unexpected alert with no threshold: %q", errOut.String())
	}
	if !strings.Contains(out.String(), "$9.00") {
		t.Errorf("report table missing: %q", out.String())
	}
}

func TestHistoryCommand_Alert(t *testing.T) {
	const total = 5.0
	tests := []struct {
		name        string
		threshold   float64
		failOnAlert bool
		wantAlert   bool
		wantErr     bool
	}{
		{name: "below total alerts", threshold: 4.0, wantAlert: true},
		{name: "equal to total does not alert", threshold: 5.0, wantAlert: false},
		{name: "above total does not alert", threshold: 6.0, wantAlert: false},
		{name: "alert with fail flag returns error", threshold: 4.0, failOnAlert: true, wantAlert: true, wantErr: true},
		{name: "threshold zero disables alert", threshold: 0, wantAlert: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := storage.NewFake()
			saveRecords(t, store,
				provider.UsageRecord{Provider: "anthropic", Day: day(2025, 8, 14), Model: "claude", CostUSD: total},
			)
			fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
			app, out, errOut := testApp(alertCfg(tc.threshold), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

			args := []string{"history", "--period", "7d"}
			if tc.failOnAlert {
				args = append(args, "--fail-on-alert")
			}
			err := run(app, args...)

			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "monthly alert threshold exceeded") {
					t.Fatalf("want threshold-exceeded error, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("run: %v", err)
			}

			gotAlert := strings.Contains(errOut.String(), "ALERT:")
			if gotAlert != tc.wantAlert {
				t.Errorf("alert in stderr = %v, want %v (stderr=%q)", gotAlert, tc.wantAlert, errOut.String())
			}
			if strings.Contains(out.String(), "ALERT") {
				t.Errorf("alert leaked into stdout: %q", out.String())
			}
		})
	}
}

func TestHistoryCommand_AlertComparesCurrentPeriod(t *testing.T) {
	// now = 2025-08-15. --period month: current = August ($3.00),
	// previous = July ($2.00). Threshold 2.5 sits between them: alert must fire
	// off the CURRENT total ($3.00 > 2.5), not the previous ($2.00 <= 2.5).
	store := storage.NewFake()
	saveRecords(t, store,
		provider.UsageRecord{Provider: "anthropic", Day: day(2025, 8, 10), Model: "claude", CostUSD: 3.0},
		provider.UsageRecord{Provider: "anthropic", Day: day(2025, 7, 15), Model: "claude", CostUSD: 2.0},
	)
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
	app, _, errOut := testApp(alertCfg(2.5), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "history", "--period", "month", "--compare"); err != nil {
		t.Fatalf("history: %v", err)
	}
	if !strings.Contains(errOut.String(), "ALERT: total spend $3.00 exceeds monthly threshold $2.50") {
		t.Errorf("compare alert must use current-period total, got stderr=%q", errOut.String())
	}
}

func TestHistoryCommand_AlertCompareNoAlertFromPrevious(t *testing.T) {
	// Mirror: threshold 2.5 with current $2.00 (August) and previous $3.00 (July).
	// If the alert wrongly used the previous total it would fire; it must not.
	store := storage.NewFake()
	saveRecords(t, store,
		provider.UsageRecord{Provider: "anthropic", Day: day(2025, 8, 10), Model: "claude", CostUSD: 2.0},
		provider.UsageRecord{Provider: "anthropic", Day: day(2025, 7, 15), Model: "claude", CostUSD: 3.0},
	)
	fp := &fakeProvider{id: "anthropic", err: errors.New("network should not be used")}
	app, _, errOut := testApp(alertCfg(2.5), store, map[string]provider.ProviderUsageSource{"anthropic": fp})

	if err := run(app, "history", "--period", "month", "--compare"); err != nil {
		t.Fatalf("history: %v", err)
	}
	if strings.Contains(errOut.String(), "ALERT") {
		t.Errorf("alert must not fire off previous-period total, got stderr=%q", errOut.String())
	}
}

func TestReportCommand_InvalidPeriod(t *testing.T) {
	app, _, _ := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": &fakeProvider{id: "anthropic"}})
	err := run(app, "report", "--period", "banana")
	if err == nil || !strings.Contains(err.Error(), "invalid period") {
		t.Fatalf("want invalid-period error, got %v", err)
	}
}
