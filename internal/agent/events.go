package agent

// Event 是 Agent 对外暴露的强类型运行事件。
type Event interface {
	// AgentEvent 限定事件只由本包定义，避免外部伪造不完整事件。
	AgentEvent()
}

// RunStartEvent 表示 Agent 已开始一次运行。
type RunStartEvent struct {
	// Input 是本次运行的用户输入摘要，已去除首尾空白。
	Input string
}

// AgentEvent 标记 RunStartEvent 为 Agent 运行事件。
func (RunStartEvent) AgentEvent() {}

// LLMRequestEvent 表示 Agent 即将向 Provider 发起一次聊天请求。
type LLMRequestEvent struct {
	// Round 是从 1 开始计数的 LLM 请求轮次。
	Round int
}

// AgentEvent 标记 LLMRequestEvent 为 Agent 运行事件。
func (LLMRequestEvent) AgentEvent() {}

// LLMErrorEvent 表示 Provider 聊天请求返回错误。
type LLMErrorEvent struct {
	// Round 是发生错误的 LLM 请求轮次。
	Round int
	// Error 保存 Provider 返回的原始错误。
	Error error
}

// AgentEvent 标记 LLMErrorEvent 为 Agent 运行事件。
func (LLMErrorEvent) AgentEvent() {}

// ToolCallEvent 表示模型已请求执行本地工具。
type ToolCallEvent struct {
	// Round 是触发该工具调用的 tool-calling 轮次，从 1 开始计数。
	Round int
	// ToolCallID 是模型生成的 tool call 标识。
	ToolCallID string
	// ToolName 是被调用的本地工具名称。
	ToolName string
	// Arguments 是模型传入工具的原始 JSON 参数。
	Arguments string
}

// AgentEvent 标记 ToolCallEvent 为 Agent 运行事件。
func (ToolCallEvent) AgentEvent() {}

// ToolResultEvent 表示本地工具已返回结果。
type ToolResultEvent struct {
	// Round 是该工具结果所属的 tool-calling 轮次，从 1 开始计数。
	Round int
	// ToolCallID 是模型生成的 tool call 标识。
	ToolCallID string
	// ToolName 是被调用的本地工具名称。
	ToolName string
	// Result 是工具成功执行后的文本结果；失败时保存可回传给模型的错误文本。
	Result string
	// Error 保存工具执行失败时的原始错误；成功时为空。
	Error error
}

// AgentEvent 标记 ToolResultEvent 为 Agent 运行事件。
func (ToolResultEvent) AgentEvent() {}

// FinalEvent 表示 Agent 已得到最终回答。
type FinalEvent struct {
	// Answer 是模型完成工具调用后返回的最终回答。
	Answer string
}

// AgentEvent 标记 FinalEvent 为 Agent 运行事件。
func (FinalEvent) AgentEvent() {}

// RunEndEvent 表示 Agent 已成功结束一次运行。
type RunEndEvent struct {
	// Answer 是运行结束时的最终回答，便于只订阅生命周期事件的调用方展示。
	Answer string
}

// AgentEvent 标记 RunEndEvent 为 Agent 运行事件。
func (RunEndEvent) AgentEvent() {}

// ErrorEvent 表示 Agent 运行因错误结束。
type ErrorEvent struct {
	// Error 保存导致运行结束的错误。
	Error error
}

// AgentEvent 标记 ErrorEvent 为 Agent 运行事件。
func (ErrorEvent) AgentEvent() {}
