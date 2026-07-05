package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"

	"harukizmoe/pimoe/internal/llms"
)

// Config is the root application configuration loaded from YAML.
type Config struct {
	// LLMs contains provider instances and the default provider selection.
	LLMs llms.Config `mapstructure:"llms"`
}

// Load reads a YAML config file and resolves environment-backed secrets.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// Read the file first so decode errors can include the exact path.
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("decode config %q: %w", path, err)
	}

	// API keys stay out of YAML; each provider names the environment variable to read.
	for name, provider := range cfg.LLMs.Providers {
		if provider.APIKeyEnv != "" {
			provider.APIKey = os.Getenv(provider.APIKeyEnv)
		}
		cfg.LLMs.Providers[name] = provider
	}

	return &cfg, nil
}
