package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadRejectsUnknownProviderKeys(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "misspelled api key env", key: "api_key_en"},
		{name: "raw api key", key: "api_key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, `llms:
  default_provider: deepseek
  providers:
    deepseek:
      type: openai_compatible
      base_url: "https://api.deepseek.com/v1"
      model: "deepseek-chat"
      api_key_env: "DEEPSEEK_API_KEY"
      `+tt.key+`: "DEEPSEEK_API_KEY"
`)

			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load() error = nil, want unknown key error")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("Load() error = %v, want mention of %q", err, tt.key)
			}
		})
	}
}

func TestLoadRejectsOpenAICompatibleMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		wantError string
	}{
		{
			name: "missing base url",
			provider: `      type: openai_compatible
      model: "deepseek-chat"
      api_key_env: "DEEPSEEK_API_KEY"`,
			wantError: "base_url",
		},
		{
			name: "missing model",
			provider: `      type: openai_compatible
      base_url: "https://api.deepseek.com/v1"
      api_key_env: "DEEPSEEK_API_KEY"`,
			wantError: "model",
		},
		{
			name: "missing api key env",
			provider: `      type: openai_compatible
      base_url: "https://api.deepseek.com/v1"
      model: "deepseek-chat"`,
			wantError: "api_key_env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, `llms:
  default_provider: deepseek
  providers:
    deepseek:
`+tt.provider+`
`)

			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load() error = nil, want missing %s error", tt.wantError)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Load() error = %v, want mention of %q", err, tt.wantError)
			}
		})
	}
}

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "providers.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
