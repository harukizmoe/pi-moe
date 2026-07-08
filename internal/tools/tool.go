package tools

import "context"

// Tool 是模型可通过 tool calling 请求执行的本地函数。
type Tool interface {
	// Name 返回暴露给模型的稳定函数名。
	Name() string
	// Description 说明模型何时应该使用该工具。
	Description() string
	// Parameters 返回工具参数的 JSON Schema object。
	Parameters() map[string]any
	// Call 使用模型传入的原始 JSON 参数执行工具。
	Call(ctx context.Context, arguments string) (string, error)
}
