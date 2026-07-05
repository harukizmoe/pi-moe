package agent

import (
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

// Agent 负责驱动一次非流式的 tool calling 主循环。
type Agent struct {
	provider llms.Provider
	tools    *tools.Registry
	model    string
}

// New 创建一个绑定固定 Provider、工具注册表和模型名的 Agent。
func New(provider llms.Provider, tools *tools.Registry, model string) *Agent {
	return &Agent{
		provider: provider,
		tools:    tools,
		model:    model,
	}
}
