package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"

	"harukizmoe/pimoe/internal/llms"
)

// Config 是从 YAML 加载的应用根配置。
type Config struct {
	// LLMs 包含 Provider 实例和默认 Provider 选择。
	LLMs llms.Config `mapstructure:"llms"`
}

// Load 读取 YAML 配置文件，并解析由环境变量承载的密钥。
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// 先读取文件，让读取错误带上明确的配置路径。
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := v.UnmarshalExact(&cfg); err != nil {
		return nil, fmt.Errorf("decode config %q: %w", path, err)
	}
	if err := validateProviderConfig(cfg); err != nil {
		return nil, fmt.Errorf("validate config %q: %w", path, err)
	}

	// API Key 不写入 YAML；每个 Provider 只声明需要读取的环境变量名。
	for name, provider := range cfg.LLMs.Providers {
		if provider.APIKeyEnv != "" {
			provider.APIKey = os.Getenv(provider.APIKeyEnv)
		}
		cfg.LLMs.Providers[name] = provider
	}

	return &cfg, nil
}

func validateProviderConfig(cfg Config) error {
	for name, provider := range cfg.LLMs.Providers {
		if provider.Type != "openai_compatible" {
			continue
		}
		if strings.TrimSpace(provider.BaseURL) == "" {
			return fmt.Errorf("provider %q openai_compatible base_url is required", name)
		}
		if strings.TrimSpace(provider.Model) == "" {
			return fmt.Errorf("provider %q openai_compatible model is required", name)
		}
		if strings.TrimSpace(provider.APIKeyEnv) == "" {
			return fmt.Errorf("provider %q openai_compatible api_key_env is required", name)
		}
	}
	return nil
}
