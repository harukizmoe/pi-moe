package tools

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

// Registry 保存本地工具，并将它们暴露为 LLM tool schema。
type Registry struct {
	tools map[string]Tool
}

// NewRegistry 创建空的工具注册表。
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register 按工具稳定名称添加或替换工具。
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// Schemas 将已注册工具转换为 OpenAI-compatible function schema。
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

// Call 将原始 JSON 参数分发给指定名称的已注册工具。
func (r *Registry) Call(ctx context.Context, name string, arguments string) (string, error) {
	tool, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	return tool.Call(ctx, arguments)
}
