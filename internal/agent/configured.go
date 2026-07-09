package agent

import (
	"context"
	"fmt"

	appconfig "harukizmoe/pimoe/internal/config"
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/tools"
)

const defaultProviderConfigPath = "configs/providers.yaml"

// Config 保存创建已装配 Agent 所需的运行时依赖配置。
type Config struct {
	// ProviderConfigPath 是 providers YAML 配置路径；为空时使用默认本地配置。
	ProviderConfigPath string
	// ProviderName 覆盖配置中的默认 Provider 实例名；为空时使用 llms.default_provider。
	ProviderName string
	// Logger 接收 Agent 内部日志；为空时使用 no-op logger。
	Logger logger.Logger
	// MaxSteps 限制一次运行最多执行多少轮 tool calling；小于 1 时使用 Agent 默认值。
	MaxSteps int
	// BaseSystemPrompt 是所有请求共享的系统级指令；为空时不注入。
	BaseSystemPrompt string
	// SessionPrompt 是当前 session 的行为设定，会追加在基础系统指令之后。
	SessionPrompt string
}

// NewConfigured 从配置文件组装 Provider、工具注册表和 Agent。
func NewConfigured(ctx context.Context, cfg Config) (*Agent, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("create agent: %w", err)
		}
	}

	path := cfg.ProviderConfigPath
	if path == "" {
		path = defaultProviderConfigPath
	}

	loaded, err := appconfig.Load(path)
	if err != nil {
		return nil, err
	}

	providerName := cfg.ProviderName
	if providerName == "" {
		providerName = loaded.LLMs.DefaultProvider
	}
	providerConfig, ok := loaded.LLMs.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", providerName)
	}

	llmRegistry := llms.NewRegistry()
	llmRegistry.Register("fake", llms.NewFakeProvider)
	llmRegistry.Register("openai_compatible", llms.NewOpenAICompatibleProvider)

	provider, err := llmRegistry.NewProvider(providerConfig)
	if err != nil {
		return nil, err
	}

	toolRegistry := tools.NewRegistry()
	toolRegistry.Register(tools.Calculator{})

	return NewWithOptions(provider, toolRegistry, providerConfig.Model, Options{
		Logger:           cfg.Logger,
		MaxSteps:         cfg.MaxSteps,
		BaseSystemPrompt: cfg.BaseSystemPrompt,
		SessionPrompt:    cfg.SessionPrompt,
	}), nil
}
