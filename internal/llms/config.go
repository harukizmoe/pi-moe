package llms

type Config struct {
	DefaultProvider string                    `mapstructure:"default_provider"`
	Providers       map[string]ProviderConfig `mapstructure:"providers"`
}

type ProviderConfig struct {
	Type           string `mapstructure:"type"`
	BaseURL        string `mapstructure:"base_url"`
	APIKeyEnv      string `mapstructure:"api_key_env"`
	APIKey         string `mapstructure:"-"`
	Model          string `mapstructure:"model"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}
