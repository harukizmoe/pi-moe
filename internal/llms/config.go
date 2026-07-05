package llms

// Config describes all LLM provider instances loaded from configs/providers.yaml.
type Config struct {
	// DefaultProvider is the provider instance name used when callers do not choose one explicitly.
	DefaultProvider string `mapstructure:"default_provider"`
	// Providers maps provider instance names, such as "deepseek", to provider configuration.
	Providers map[string]ProviderConfig `mapstructure:"providers"`
}

// ProviderConfig contains the provider implementation type and runtime connection settings.
type ProviderConfig struct {
	// Type selects the provider implementation registered in Registry, for example "openai_compatible" or "fake".
	Type string `mapstructure:"type"`
	// BaseURL is the OpenAI-compatible endpoint root, usually ending in /v1.
	BaseURL string `mapstructure:"base_url"`
	// APIKeyEnv names the environment variable that holds the provider API key.
	APIKeyEnv string `mapstructure:"api_key_env"`
	// APIKey is populated at load time from APIKeyEnv and is never read directly from YAML.
	APIKey string `mapstructure:"-"`
	// Model is the wire model name sent to the provider.
	Model string `mapstructure:"model"`
	// TimeoutSeconds bounds provider HTTP calls and fake-provider waits.
	TimeoutSeconds int `mapstructure:"timeout_seconds"`
}
