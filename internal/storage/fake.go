package storage

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/akomyagin/aiCostTracker/internal/provider"
)

// Fake is an in-memory Store for unit tests of code that persists snapshots,
// with the same idempotent-upsert semantics as the SQLite store but no disk.
type Fake struct {
	mu   sync.Mutex
	rows map[fakeKey]provider.UsageRecord
}

type fakeKey struct {
	provider string
	day      string // YYYY-MM-DD UTC
	model    string
}

var _ Store = (*Fake)(nil)

// NewFake returns an empty in-memory Store.
func NewFake() *Fake {
	return &Fake{rows: map[fakeKey]provider.UsageRecord{}}
}

// Save upserts each record by (provider, day, model), mirroring the SQLite store.
func (f *Fake) Save(_ context.Context, snap provider.Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range snap.Records {
		day := r.Day.UTC().Truncate(24 * time.Hour)
		rec := r
		rec.Day = day
		f.rows[fakeKey{provider: r.Provider, day: day.Format(dayLayout), model: r.Model}] = rec
	}
	return nil
}

// Query returns records in [Start, End), optionally filtered by provider, sorted
// (day, provider, model).
func (f *Fake) Query(_ context.Context, providerID string, w provider.Window) ([]provider.UsageRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	start := w.Start.UTC().Truncate(24 * time.Hour)
	end := w.End.UTC().Truncate(24 * time.Hour)

	var out []provider.UsageRecord
	for _, rec := range f.rows {
		if providerID != "" && rec.Provider != providerID {
			continue
		}
		if rec.Day.Before(start) || !rec.Day.Before(end) {
			continue
		}
		out = append(out, rec)
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].Day.Equal(out[j].Day) {
			return out[i].Day.Before(out[j].Day)
		}
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}

// Close is a no-op for the in-memory store.
func (f *Fake) Close() error { return nil }
