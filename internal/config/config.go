// Package config loads aiCostTracker configuration.
//
// Config lives at os.UserConfigDir()/aicost/config.yaml (never a hardcoded
// ~/.config). Admin/usage API keys may also come from environment variables so
// they never have to be written to disk — env overrides the file. Secrets are
// never logged.
//
// Реализация загрузки/валидации — Этап 1 (см. docs/TECHNICAL_PLAN.md §5).
package config

import "time"

// ProviderConfig holds the credentials and endpoint overrides for one provider.
// AdminKey is the admin/org-level key required by usage/cost APIs — distinct
// from a model-calling key. BaseURL allows pointing at a proxy or a test server.
type ProviderConfig struct {
	Enabled  bool   `yaml:"enabled"`
	AdminKey string `yaml:"admin_key"` // may be supplied via env instead; never logged
	BaseURL  string `yaml:"base_url"`  // optional override; empty = provider default
}

// Config is the whole resolved configuration.
type Config struct {
	// Providers is keyed by stable provider id ("anthropic", "openai", …),
	// matching ProviderUsageSource.ID().
	Providers map[string]ProviderConfig `yaml:"providers"`

	// HTTPTimeout and MaxRetries govern every provider HTTP call (retry uses
	// exponential backoff + jitter; see the go-cost-tracker-dev skill).
	HTTPTimeout time.Duration `yaml:"http_timeout"`
	MaxRetries  int           `yaml:"max_retries"`

	// DBPath is where the SQLite history lives; empty = default under
	// os.UserConfigDir()/aicost/history.db.
	DBPath string `yaml:"db_path"`
}

// Load reads and validates configuration from disk + environment.
//
// Реализация — Этап 1.
func Load() (Config, error) {
	return Config{}, nil
}
