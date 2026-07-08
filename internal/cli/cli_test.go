package cli

import (
	"bytes"
	"context"
	"errors"
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

func TestReportCommand_InvalidPeriod(t *testing.T) {
	app, _, _ := testApp(enabledCfg(), storage.NewFake(), map[string]provider.ProviderUsageSource{"anthropic": &fakeProvider{id: "anthropic"}})
	err := run(app, "report", "--period", "banana")
	if err == nil || !strings.Contains(err.Error(), "invalid period") {
		t.Fatalf("want invalid-period error, got %v", err)
	}
}
