package tools

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

// Registry stores local tools and exposes them as LLM tool schemas.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds or replaces a tool by its stable name.
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// Schemas converts registered tools into OpenAI-compatible function schemas.
func (r *Registry) Schemas() []llms.Tool {
	schemas := make([]llms.Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		schemas = append(schemas, llms.Tool{
			Type: "function",
			Function: llms.ToolFunction{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}
	return schemas
}

// Call dispatches raw JSON arguments to the named registered tool.
func (r *Registry) Call(ctx context.Context, name string, arguments string) (string, error) {
	tool, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	return tool.Call(ctx, arguments)
}
