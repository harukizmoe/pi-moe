package agent

import (
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/tools"
)

const defaultMaxSteps = 4

// Options 保存 Agent harness core 的可选运行配置。
type Options struct {
	// Logger 接收 Agent 内部结构化日志；为空时使用 no-op logger。
	Logger logger.Logger
	// MaxSteps 限制一次 Run 中最多执行多少轮 tool calling；小于 1 时使用默认值。
	MaxSteps int
}

// Agent 负责驱动一次非流式的 tool calling 主循环。
type Agent struct {
	provider llms.Provider
	tools    *tools.Registry
	model    string
	logger   logger.Logger
	maxSteps int
}

// New 创建一个绑定固定 Provider、工具注册表和模型名的 Agent。
func New(provider llms.Provider, tools *tools.Registry, model string) *Agent {
	return NewWithOptions(provider, tools, model, Options{})
}

// NewWithLogger 创建一个带显式 logger 的 Agent。
func NewWithLogger(provider llms.Provider, tools *tools.Registry, model string, log logger.Logger) *Agent {
	return NewWithOptions(provider, tools, model, Options{Logger: log})
}

// NewWithOptions 创建一个带显式运行选项的 Agent。
func NewWithOptions(provider llms.Provider, tools *tools.Registry, model string, opts Options) *Agent {
	log := opts.Logger
	if log == nil {
		log = logger.NewNoop()
	}
	maxSteps := opts.MaxSteps
	if maxSteps < 1 {
		maxSteps = defaultMaxSteps
	}

	return &Agent{
		provider: provider,
		tools:    tools,
		model:    model,
		logger:   log,
		maxSteps: maxSteps,
	}
}
