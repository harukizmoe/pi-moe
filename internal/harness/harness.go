package harness

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/agent"
	appconfig "harukizmoe/pimoe/internal/config"
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/tools"
)

const defaultProviderConfigPath = "configs/providers.yaml"

// Config 保存创建 Agent harness 所需的运行时依赖配置。
type Config struct {
	// ProviderConfigPath 是 providers YAML 配置路径；为空时使用默认本地配置。
	ProviderConfigPath string
	// Logger 接收 Agent 内部日志；为空时使用 no-op logger。
	Logger logger.Logger
	// MaxSteps 限制一次运行最多执行多少轮 tool calling；小于 1 时使用 Agent 默认值。
	MaxSteps int
	// OnEvent 接收 Agent 运行事件；为空时不发送事件。
	OnEvent agent.EventHandler
}

// Harness 是后端入口可复用的 Agent 运行内核。
type Harness struct {
	agent *agent.Agent
}

// New 从配置文件组装 Provider、工具注册表和 Agent。
func New(ctx context.Context, cfg Config) (*Harness, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("create harness: %w", err)
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

	providerName := loaded.LLMs.DefaultProvider
	providerConfig, ok := loaded.LLMs.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown default provider %q", providerName)
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

	runner := agent.NewWithOptions(provider, toolRegistry, providerConfig.Model, agent.Options{
		Logger:   cfg.Logger,
		MaxSteps: cfg.MaxSteps,
		OnEvent:  cfg.OnEvent,
	})

	return &Harness{agent: runner}, nil
}

// Run 执行一次 Agent 运行，并返回结构化结果。
func (h *Harness) Run(ctx context.Context, input string) (*agent.RunResult, error) {
	return h.agent.RunResult(ctx, input)
}
