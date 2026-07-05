package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProvidersConfig(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")

	dir := t.TempDir()
	path := filepath.Join(dir, "providers.yaml")
	content := []byte(`llms:
  default_provider: deepseek
  providers:
    deepseek:
      type: openai_compatible
      base_url: "https://api.deepseek.com/v1"
      api_key_env: "DEEPSEEK_API_KEY"
      model: "deepseek-chat"
      timeout_seconds: 60
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.LLMs.DefaultProvider != "deepseek" {
		t.Fatalf("default provider = %q", cfg.LLMs.DefaultProvider)
	}
	provider := cfg.LLMs.Providers["deepseek"]
	if provider.Type != "openai_compatible" {
		t.Fatalf("provider type = %q", provider.Type)
	}
	if provider.APIKey != "test-key" {
		t.Fatalf("api key = %q", provider.APIKey)
	}
}
