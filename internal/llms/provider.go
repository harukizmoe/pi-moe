package llms

import (
	"context"
	"fmt"
)

type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

type Factory func(cfg ProviderConfig) (Provider, error)

type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

func (r *Registry) Register(providerType string, factory Factory) {
	r.factories[providerType] = factory
}

func (r *Registry) NewProvider(cfg ProviderConfig) (Provider, error) {
	factory, ok := r.factories[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown llm provider type %q", cfg.Type)
	}

	return factory(cfg)
}
