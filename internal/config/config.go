package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"

	"harukizmoe/pimoe/internal/llms"
)

type Config struct {
	LLMs llms.Config `mapstructure:"llms"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("decode config %q: %w", path, err)
	}

	for name, provider := range cfg.LLMs.Providers {
		if provider.APIKeyEnv != "" {
			provider.APIKey = os.Getenv(provider.APIKeyEnv)
		}
		cfg.LLMs.Providers[name] = provider
	}

	return &cfg, nil
}
