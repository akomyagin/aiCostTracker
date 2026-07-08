// Package config loads aiCostTracker configuration.
//
// Config lives at os.UserConfigDir()/aicost/config.yaml (never a hardcoded
// ~/.config). Admin/usage API keys may also come from environment variables so
// they never have to be written to disk — env overrides the file. Secrets are
// never logged.
//
// See docs/TECHNICAL_PLAN.md §5.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

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

// Defaults applied when the config file omits a field or does not exist.
const (
	defaultHTTPTimeout = 30 * time.Second
	defaultMaxRetries  = 4
)

// knownProviders lists provider ids the CLI understands. Env overrides are only
// applied for these, so an unknown key in the file is a hard validation error.
var knownProviders = []string{"anthropic", "openai"}

// Load reads and validates configuration from disk + environment.
//
// Resolution order: file defaults are filled first, then environment variables
// override admin keys (AICOST_<PROVIDER>_ADMIN_KEY). A missing config file is not
// an error — env-only operation is supported.
func Load() (Config, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return Config{}, err
	}
	return loadFrom(path, os.Getenv)
}

// loadFrom is the testable core: it reads the file at path (absent = defaults
// only) and applies env overrides via getenv (injected for tests).
func loadFrom(path string, getenv func(string) string) (Config, error) {
	cfg := Config{Providers: map[string]ProviderConfig{}}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	case os.IsNotExist(err):
		// No file: run from defaults + env only.
	default:
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	applyDefaults(&cfg)
	applyEnvOverrides(&cfg, getenv)

	if err := validate(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyDefaults fills zero-valued global fields with defaults.
func applyDefaults(cfg *Config) {
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = defaultHTTPTimeout
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
}

// applyEnvOverrides lets AICOST_<PROVIDER>_ADMIN_KEY override the file's admin
// key (and implicitly enable that provider) so secrets need not touch disk.
func applyEnvOverrides(cfg *Config, getenv func(string) string) {
	for _, id := range knownProviders {
		envKey := "AICOST_" + strings.ToUpper(id) + "_ADMIN_KEY"
		if v := getenv(envKey); v != "" {
			pc := cfg.Providers[id]
			pc.AdminKey = v
			pc.Enabled = true // an env key means the user wants this provider active
			cfg.Providers[id] = pc
		}
	}
}

// validate rejects configurations that would fail confusingly at fetch time.
// It never includes any admin key in an error message.
func validate(cfg *Config) error {
	if cfg.MaxRetries < 0 {
		return fmt.Errorf("max_retries must be >= 0, got %d", cfg.MaxRetries)
	}
	for id := range cfg.Providers {
		if !isKnownProvider(id) {
			return fmt.Errorf("unknown provider %q in config (known: %s)", id, strings.Join(knownProviders, ", "))
		}
	}
	for id, pc := range cfg.Providers {
		if pc.Enabled && pc.AdminKey == "" {
			return fmt.Errorf("provider %q is enabled but has no admin_key (set it in config or via AICOST_%s_ADMIN_KEY)",
				id, strings.ToUpper(id))
		}
	}
	return nil
}

func isKnownProvider(id string) bool {
	for _, k := range knownProviders {
		if k == id {
			return true
		}
	}
	return false
}

// EnabledProviders returns the ids of enabled providers in a stable order.
func (c Config) EnabledProviders() []string {
	var out []string
	for _, id := range knownProviders {
		if pc, ok := c.Providers[id]; ok && pc.Enabled {
			out = append(out, id)
		}
	}
	return out
}

// ResolvedDBPath returns the configured DBPath or the default under the user
// config dir.
func (c Config) ResolvedDBPath() (string, error) {
	if c.DBPath != "" {
		return c.DBPath, nil
	}
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "history.db"), nil
}

// DefaultConfigPath returns os.UserConfigDir()/aicost/config.yaml.
func DefaultConfigPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// configDir returns os.UserConfigDir()/aicost, cross-platform.
func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(base, "aicost"), nil
}
