// Package provider defines the ProviderUsageSource port and its adapters.
//
// A ProviderUsageSource fetches usage/cost data for one AI provider (Anthropic,
// OpenAI, …) over a time window. Each provider exposes a different admin/usage
// API, so every provider is an adapter behind this single interface — adding a
// new provider means adding one file here, not touching the CLI, storage or
// report layers (ports & adapters, cf. gitl / KnowledgeVault).
//
// Реализация адаптеров — Этап 1+ (см. docs/TECHNICAL_PLAN.md §6).
package provider

import (
	"context"
	"time"
)

// Window is a half-open time range [Start, End) for which usage is requested.
// Providers bucket usage by UTC day, so callers should pass day-aligned bounds.
type Window struct {
	Start time.Time
	End   time.Time
}

// UsageRecord is one normalized usage/cost data point, already converted from a
// provider's native shape into aiCostTracker's common model. One record is a
// single (provider, day, model) triple so history can be re-aggregated later.
type UsageRecord struct {
	Provider     string    // stable provider id, e.g. "anthropic", "openai"
	Day          time.Time // UTC day (00:00) this record accounts for
	Model        string    // model id if the provider reports it, else ""
	InputTokens  int64     // 0 if the provider does not break tokens out
	OutputTokens int64
	CostUSD      float64 // cost in USD as reported/derived; 0 if unknown
}

// Snapshot is the full result of one fetch for one provider over one Window:
// the normalized records plus the moment they were captured. Snapshots are what
// storage persists so trends can be computed across runs.
type Snapshot struct {
	Provider  string
	Window    Window
	Records   []UsageRecord
	FetchedAt time.Time
}

// ProviderUsageSource is the port every provider adapter implements.
//
// Contract:
//   - ID returns a stable, lowercase identifier used as a storage key and CLI
//     selector (e.g. "anthropic"). It must be constant for the adapter's life.
//   - Fetch retrieves usage for the given Window. It must honour ctx cancellation,
//     retry retryable failures (429/5xx/network) with exponential backoff + jitter,
//     and fail fast on fatal ones (400/401/403). It must never log the API key.
//
// The interface is intentionally introduced with the *first* real adapter, per
// the portfolio rule "an interface appears on the second implementation" — here
// two providers (Anthropic + OpenAI) are in scope from Этап 1, so the port earns
// its place immediately.
type ProviderUsageSource interface {
	ID() string
	Fetch(ctx context.Context, w Window) (Snapshot, error)
}
