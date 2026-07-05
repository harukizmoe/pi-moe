package agent

import (
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/tools"
)

// Agent 负责驱动一次非流式的 tool calling 主循环。
type Agent struct {
	provider llms.Provider
	tools    *tools.Registry
	model    string
	logger   logger.Logger
}

// New 创建一个绑定固定 Provider、工具注册表和模型名的 Agent。
func New(provider llms.Provider, tools *tools.Registry, model string) *Agent {
	return NewWithLogger(provider, tools, model, logger.NewNoop())
}

// NewWithLogger 创建一个带显式 logger 的 Agent。
func NewWithLogger(provider llms.Provider, tools *tools.Registry, model string, log logger.Logger) *Agent {
	if log == nil {
		log = logger.NewNoop()
	}

	return &Agent{
		provider: provider,
		tools:    tools,
		model:    model,
		logger:   log,
	}
}
