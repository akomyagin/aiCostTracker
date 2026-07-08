package provider

import (
	"context"
	"fmt"
)

// Anthropic is the ProviderUsageSource adapter for Anthropic's Usage & Cost
// Admin API.
//
// [ASSUMPTION] Anthropic exposes an admin-scoped Usage & Cost API that requires
// an admin/org key (distinct from the model-calling API key). The exact
// endpoints, auth header and response shape must be confirmed against live docs
// in Этап 1 — see docs/TECHNICAL_PLAN.md §4. Do NOT hardcode invented endpoints
// as fact until then; this stub only fixes the identity and config surface.
//
// Реализация — Этап 1.
type Anthropic struct {
	adminKey string // org/admin-level key; never logged
}

var _ ProviderUsageSource = (*Anthropic)(nil)

// NewAnthropic builds the adapter. adminKey is the admin/org key required by the
// Usage & Cost Admin API, not the ordinary model API key.
func NewAnthropic(adminKey string) *Anthropic {
	return &Anthropic{adminKey: adminKey}
}

// ID returns the stable provider identifier.
func (a *Anthropic) ID() string { return "anthropic" }

// Fetch is not yet implemented — Этап 1.
func (a *Anthropic) Fetch(ctx context.Context, w Window) (Snapshot, error) {
	return Snapshot{}, fmt.Errorf("provider anthropic: Fetch not implemented (Этап 1)")
}
