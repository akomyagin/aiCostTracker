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

func TestReportCommand_InvalidPeriod(t *testing.T) {
	app, _, _ := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": &fakeProvider{id: "anthropic"}})
	err := run(app, "report", "--period", "banana")
	if err == nil || !strings.Contains(err.Error(), "invalid period") {
		t.Fatalf("want invalid-period error, got %v", err)
	}
}
