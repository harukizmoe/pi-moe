package agent

import "time"

// Event 是 Agent 对外暴露的强类型运行事件。
type Event interface {
	// AgentEvent 限定事件只由本包定义，避免外部伪造不完整事件。
	AgentEvent()
}

// MessageDeltaKind 标识一段 message 增量属于可见文本、thinking 还是 tool call 参数。
type MessageDeltaKind string

const (
	// MessageDeltaText 表示 assistant 可见文本增量。
	MessageDeltaText MessageDeltaKind = "text"
	// MessageDeltaThinking 表示模型 reasoning/thinking 增量；当前 provider 暂不产生。
	MessageDeltaThinking MessageDeltaKind = "thinking"
	// MessageDeltaToolCall 表示 assistant tool call 参数增量。
	MessageDeltaToolCall MessageDeltaKind = "tool_call"
)

// RunStartEvent 表示 Agent 已开始一次运行。
type RunStartEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
}

// AgentEvent 标记 RunStartEvent 为 Agent 运行事件。
func (RunStartEvent) AgentEvent() {}

// TurnStartEvent 表示 Agent 已开始处理当前用户 turn。
type TurnStartEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// Turn 是 transcript 中从 1 开始计数的当前用户 turn。
	Turn int
	// UserMessage 是触发本轮运行的用户消息。
	UserMessage UserMessage
}

// AgentEvent 标记 TurnStartEvent 为 Agent 运行事件。
func (TurnStartEvent) AgentEvent() {}

// MessageStartEvent 表示一条 assistant message 开始生成。
type MessageStartEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// MessageID 是本条 assistant message 在本次运行内的稳定标识。
	MessageID string
	// Role 是消息角色；当前只会是 "assistant"。
	Role string
}

// AgentEvent 标记 MessageStartEvent 为 Agent 运行事件。
func (MessageStartEvent) AgentEvent() {}

// MessageDeltaEvent 表示一条 assistant message 的增量内容。
type MessageDeltaEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// MessageID 关联对应的 MessageStartEvent 和 MessageEndEvent。
	MessageID string
	// Kind 标识增量类型。
	Kind MessageDeltaKind
	// ContentIndex 是同类内容块在当前 message 中的下标。
	ContentIndex int
	// Delta 是本次输出的增量文本；非 streaming provider 可一次性输出完整内容。
	Delta string
}

// AgentEvent 标记 MessageDeltaEvent 为 Agent 运行事件。
func (MessageDeltaEvent) AgentEvent() {}

// MessageEndEvent 表示一条 assistant message 已完整生成。
type MessageEndEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// MessageID 关联对应的 MessageStartEvent。
	MessageID string
	// Message 是可持久化到 transcript 的完整 assistant message。
	Message AssistantMessage
}

// AgentEvent 标记 MessageEndEvent 为 Agent 运行事件。
func (MessageEndEvent) AgentEvent() {}

// ToolExecutionStartEvent 表示 Runtime 已开始治理一次模型 tool call。
type ToolExecutionStartEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// ToolCallID 是模型生成的 tool call 标识。
	ToolCallID string
	// ToolName 是被调用的稳定工具名称。
	ToolName string
	// ToolVersion 是 request-scoped AllowedTool 声明的版本；未知或未授权时为空。
	ToolVersion string
	// ArgumentsDigest 是原始参数的 SHA-256 digest。
	ArgumentsDigest string
	// StartedAt 是 UTC 调用开始时间。
	StartedAt time.Time
}

// AgentEvent 标记 ToolExecutionStartEvent 为 Agent 运行事件。
func (ToolExecutionStartEvent) AgentEvent() {}

// ToolExecutionEndEvent 表示一次 tool call 已产生配对 ToolResultMessage。
type ToolExecutionEndEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// ToolCallID 是模型生成的 tool call 标识。
	ToolCallID string
	// ToolName 是稳定工具名称。
	ToolName string
	// ToolVersion 是 AllowedTool 版本；未知工具时为空。
	ToolVersion string
	// Status 是调用的稳定终态。
	Status ToolResultStatus
	// Result 是可持久化到 transcript 的安全 tool result message。
	Result ToolResultMessage
	// ArgumentsDigest 和 OutputDigest 是输入、模型可见输出的 SHA-256 digest。
	ArgumentsDigest string
	OutputDigest    string
	// InternalDigest 是 executor 内部细节或分类错误的 SHA-256 digest，不含原文。
	InternalDigest string
	// StartedAt 和 EndedAt 是 UTC 生命周期边界。
	StartedAt time.Time
	EndedAt   time.Time
	// Error 仅保存稳定安全分类，不包含 executor 原始错误正文。
	Error error
}

// AgentEvent 标记 ToolExecutionEndEvent 为 Agent 运行事件。
func (ToolExecutionEndEvent) AgentEvent() {}

// ToolApprovalRequestedEvent 表示 Runtime 在执行前请求 request-scoped 审批。
type ToolApprovalRequestedEvent struct {
	RunID           string
	ToolCallID      string
	ToolName        string
	ToolVersion     string
	ArgumentsDigest string
	RequestedAt     time.Time
}

// AgentEvent 标记 ToolApprovalRequestedEvent 为 Agent 运行事件。
func (ToolApprovalRequestedEvent) AgentEvent() {}

// ToolApprovalDecidedEvent 表示 request-scoped 审批已完成。
type ToolApprovalDecidedEvent struct {
	RunID       string
	ToolCallID  string
	ToolName    string
	ToolVersion string
	Approved    bool
	Decision    string
	DecidedAt   time.Time
}

// AgentEvent 标记 ToolApprovalDecidedEvent 为 Agent 运行事件。
func (ToolApprovalDecidedEvent) AgentEvent() {}

// TurnEndEvent 表示当前用户 turn 已结束。
type TurnEndEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// Turn 是 transcript 中从 1 开始计数的当前用户 turn。
	Turn int
}

// AgentEvent 标记 TurnEndEvent 为 Agent 运行事件。
func (TurnEndEvent) AgentEvent() {}

// RunEndEvent 表示 Agent 已成功结束一次运行。
type RunEndEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
}

// AgentEvent 标记 RunEndEvent 为 Agent 运行事件。
func (RunEndEvent) AgentEvent() {}

// ErrorEvent 表示 Agent 运行因错误结束。
type ErrorEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识；输入校验失败时可能为空。
	RunID string
	// Error 保存导致运行结束的错误。
	Error error
}

// AgentEvent 标记 ErrorEvent 为 Agent 运行事件。
func (ErrorEvent) AgentEvent() {}
