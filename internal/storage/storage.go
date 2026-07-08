// Package storage persists usage snapshots locally in SQLite so aiCostTracker
// can show trends over time, not just the current period. The DB file is git-
// ignored (it is personal spend data) and lives under os.UserConfigDir().
//
// The concrete SQLite implementation uses modernc.org/sqlite (pure Go, no CGO),
// so cross-compilation stays painless. The Store interface is declared here
// because there are two obvious implementations — SQLite and an in-memory fake
// for tests — which justifies the port per the "interface on the second
// implementation" rule.
package storage

import (
	"context"

	"github.com/akomyagin/aiCostTracker/internal/provider"
)

// Store persists and queries usage snapshots.
type Store interface {
	// Save records one provider snapshot. Re-saving the same (provider, day,
	// model) rows must be idempotent (upsert) so re-fetching a period does not
	// double-count history.
	Save(ctx context.Context, s provider.Snapshot) error

	// Query returns records for the given provider within the window. An empty
	// provider means all providers.
	Query(ctx context.Context, providerID string, w provider.Window) ([]provider.UsageRecord, error)

	// Close releases the underlying handle.
	Close() error
}
