package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// noEnv is a getenv that returns nothing.
func noEnv(string) string { return "" }

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadFrom_FileParsed(t *testing.T) {
	path := writeConfig(t, `
http_timeout: 15s
max_retries: 2
providers:
  anthropic:
    enabled: true
    admin_key: file-key
  openai:
    enabled: false
`)
	cfg, err := loadFrom(path, noEnv)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if cfg.HTTPTimeout != 15*time.Second {
		t.Errorf("HTTPTimeout = %v", cfg.HTTPTimeout)
	}
	if cfg.MaxRetries != 2 {
		t.Errorf("MaxRetries = %d", cfg.MaxRetries)
	}
	if cfg.Providers["anthropic"].AdminKey != "file-key" {
		t.Errorf("anthropic admin_key = %q", cfg.Providers["anthropic"].AdminKey)
	}
	if got := cfg.EnabledProviders(); len(got) != 1 || got[0] != "anthropic" {
		t.Errorf("EnabledProviders = %v, want [anthropic]", got)
	}
}

func TestLoadFrom_EnvOverridesAndEnables(t *testing.T) {
	path := writeConfig(t, `
providers:
  openai:
    enabled: false
`)
	env := func(k string) string {
		if k == "AICOST_OPENAI_ADMIN_KEY" {
			return "env-secret"
		}
		return ""
	}
	cfg, err := loadFrom(path, env)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	pc := cfg.Providers["openai"]
	if pc.AdminKey != "env-secret" {
		t.Errorf("admin_key = %q, want env-secret", pc.AdminKey)
	}
	if !pc.Enabled {
		t.Error("env key should enable the provider")
	}
}

func TestLoadFrom_EnvEnablesOpenRouter(t *testing.T) {
	env := func(k string) string {
		if k == "AICOST_OPENROUTER_ADMIN_KEY" {
			return "sk-or-mgmt-secret"
		}
		return ""
	}
	cfg, err := loadFrom(filepath.Join(t.TempDir(), "absent.yaml"), env)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	pc := cfg.Providers["openrouter"]
	if pc.AdminKey != "sk-or-mgmt-secret" {
		t.Errorf("admin_key = %q, want sk-or-mgmt-secret", pc.AdminKey)
	}
	if !pc.Enabled {
		t.Error("env key should enable openrouter")
	}
	got := cfg.EnabledProviders()
	if len(got) != 1 || got[0] != "openrouter" {
		t.Errorf("EnabledProviders = %v, want [openrouter]", got)
	}
}

func TestLoadFrom_MissingFileUsesDefaults(t *testing.T) {
	cfg, err := loadFrom(filepath.Join(t.TempDir(), "absent.yaml"), noEnv)
	if err != nil {
		t.Fatalf("loadFrom missing file: %v", err)
	}
	if cfg.HTTPTimeout != defaultHTTPTimeout {
		t.Errorf("HTTPTimeout = %v, want default", cfg.HTTPTimeout)
	}
	if cfg.MaxRetries != defaultMaxRetries {
		t.Errorf("MaxRetries = %d, want default", cfg.MaxRetries)
	}
	if len(cfg.EnabledProviders()) != 0 {
		t.Errorf("expected no providers by default")
	}
}

func TestLoadFrom_EnabledWithoutKeyFails(t *testing.T) {
	path := writeConfig(t, `
providers:
  anthropic:
    enabled: true
`)
	_, err := loadFrom(path, noEnv)
	if err == nil {
		t.Fatal("expected error: enabled provider without admin_key")
	}
}

func TestLoadFrom_UnknownProviderFails(t *testing.T) {
	path := writeConfig(t, `
providers:
  gemini:
    enabled: true
    admin_key: x
`)
	if _, err := loadFrom(path, noEnv); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestLoadFrom_AlertThreshold(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
		want    float64
	}{
		{
			name: "positive parses",
			body: "alert:\n  monthly_usd: 200\n",
			want: 200,
		},
		{
			name: "absent block is zero (disabled)",
			body: "max_retries: 2\n",
			want: 0,
		},
		{
			name:    "negative is a validation error",
			body:    "alert:\n  monthly_usd: -1\n",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.body)
			cfg, err := loadFrom(path, noEnv)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected validation error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadFrom: %v", err)
			}
			if cfg.Alert.MonthlyUSD != tc.want {
				t.Errorf("Alert.MonthlyUSD = %g, want %g", cfg.Alert.MonthlyUSD, tc.want)
			}
		})
	}
}

func TestResolvedDBPath_Explicit(t *testing.T) {
	cfg := Config{DBPath: "/tmp/custom.db"}
	got, err := cfg.ResolvedDBPath()
	if err != nil {
		t.Fatalf("ResolvedDBPath: %v", err)
	}
	if got != "/tmp/custom.db" {
		t.Errorf("got %q", got)
	}
}

func TestResolvedDBPath_Default(t *testing.T) {
	cfg := Config{}
	got, err := cfg.ResolvedDBPath()
	if err != nil {
		t.Fatalf("ResolvedDBPath: %v", err)
	}
	if filepath.Base(got) != "history.db" {
		t.Errorf("default db path base = %q, want history.db", filepath.Base(got))
	}
}
