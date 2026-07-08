package llms

import (
	"context"
	"fmt"
)

// Provider 是所有 LLM 后端必须实现的流式接口。
type Provider interface {
	// ChatStream 发送一次标准化聊天请求，并返回 provider-neutral streaming 事件。
	ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error)
}

// Factory 根据一个 Provider 实例配置创建 Provider。
type Factory func(cfg ProviderConfig) (Provider, error)

// Registry 将 Provider 实现类型映射到工厂函数。
type Registry struct {
	factories map[string]Factory
}

// NewRegistry 创建空的 Provider 注册表。
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Register 将 Provider 实现类型（例如 "fake"）绑定到对应工厂。
func (r *Registry) Register(providerType string, factory Factory) {
	r.factories[providerType] = factory
}

// NewProvider 创建 cfg.Type 选中的 Provider。
func (r *Registry) NewProvider(cfg ProviderConfig) (Provider, error) {
	// Provider 实例通过 type 选择实现，因此 "deepseek" 这类实例名可以复用 "openai_compatible"。
	factory, ok := r.factories[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown llm provider type %q", cfg.Type)
	}

	return factory(cfg)
}
