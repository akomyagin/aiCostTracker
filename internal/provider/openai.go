package provider

import (
	"context"
	"fmt"
)

// OpenAI is the ProviderUsageSource adapter for OpenAI's Usage API.
//
// [ASSUMPTION] OpenAI exposes a Usage/Costs API that requires an admin/org-level
// key (an Admin key), distinct from a standard sk- API key. Exact endpoints,
// pagination and bucketing must be confirmed against live docs in Этап 1 — see
// docs/TECHNICAL_PLAN.md §4. This stub fixes only the identity and config
// surface; no endpoints are hardcoded until verified.
//
// Реализация — Этап 1.
type OpenAI struct {
	adminKey string // admin/org key; never logged
}

var _ ProviderUsageSource = (*OpenAI)(nil)

// NewOpenAI builds the adapter. adminKey is the admin/org key required by the
// Usage API, not a normal model API key.
func NewOpenAI(adminKey string) *OpenAI {
	return &OpenAI{adminKey: adminKey}
}

// ID returns the stable provider identifier.
func (o *OpenAI) ID() string { return "openai" }

// Fetch is not yet implemented — Этап 1.
func (o *OpenAI) Fetch(ctx context.Context, w Window) (Snapshot, error) {
	return Snapshot{}, fmt.Errorf("provider openai: Fetch not implemented (Этап 1)")
}
