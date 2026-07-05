package tools

import "context"

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Call(ctx context.Context, arguments string) (string, error)
}
