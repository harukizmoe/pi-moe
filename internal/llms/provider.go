package llms

import (
	"context"
	"fmt"
)

// Provider is the minimal interface every LLM backend must implement.
type Provider interface {
	// Chat sends one normalized chat request and returns one normalized assistant message.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// Factory builds a Provider from one provider instance config.
type Factory func(cfg ProviderConfig) (Provider, error)

// Registry maps provider implementation types to factories.
type Registry struct {
	factories map[string]Factory
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Register binds a provider implementation type, such as "fake", to its factory.
func (r *Registry) Register(providerType string, factory Factory) {
	r.factories[providerType] = factory
}

// NewProvider creates the Provider selected by cfg.Type.
func (r *Registry) NewProvider(cfg ProviderConfig) (Provider, error) {
	// Provider instances choose implementations by type so names like "deepseek" can share "openai_compatible".
	factory, ok := r.factories[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown llm provider type %q", cfg.Type)
	}

	return factory(cfg)
}
