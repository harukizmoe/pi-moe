package agent

import (
	"strings"

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
	// BaseSystemPrompt 是所有请求共享的系统级指令；不会进入调用方 transcript。
	BaseSystemPrompt string
	// SessionPrompt 是当前 session 的行为设定，会追加在基础系统指令之后。
	SessionPrompt string
	// Context 配置每次 Provider 调用前的预算估算、完整 turn 裁剪和显式压缩策略。
	Context ContextOptions
}

// Agent 负责驱动一次基于事件流的 tool calling 主循环。
type Agent struct {
	provider      llms.Provider
	tools         *tools.Registry
	model         string
	logger        logger.Logger
	maxSteps      int
	basePrompt    string
	sessionPrompt string
	context       contextPolicy
}

// New 创建一个绑定固定 Provider、工具注册表和模型名的 Agent。
func New(provider llms.Provider, registry *tools.Registry, model string) *Agent {
	return NewWithOptions(provider, registry, model, Options{})
}

// NewWithLogger 创建一个带显式 logger 的 Agent。
func NewWithLogger(provider llms.Provider, registry *tools.Registry, model string, log logger.Logger) *Agent {
	return NewWithOptions(provider, registry, model, Options{Logger: log})
}

// NewWithOptions 创建一个带显式运行选项的 Agent。
func NewWithOptions(provider llms.Provider, registry *tools.Registry, model string, opts Options) *Agent {
	log := opts.Logger
	if log == nil {
		log = logger.NewNoop()
	}
	maxSteps := opts.MaxSteps
	if maxSteps < 1 {
		maxSteps = defaultMaxSteps
	}
	basePrompt := strings.TrimSpace(opts.BaseSystemPrompt)
	sessionPrompt := strings.TrimSpace(opts.SessionPrompt)
	if registry == nil {
		registry = tools.NewRegistry()
	}

	return &Agent{
		provider:      provider,
		tools:         registry,
		model:         model,
		logger:        log,
		maxSteps:      maxSteps,
		basePrompt:    basePrompt,
		sessionPrompt: sessionPrompt,
		context:       newContextPolicy(opts.Context),
	}
}
