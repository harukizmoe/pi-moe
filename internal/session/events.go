package session

import "harukizmoe/pimoe/internal/agent"

// Event 描述 Session 对外转发的 Agent 运行事件。
type Event = agent.Event

// RunCompletedEvent 表示 Runtime 成功完成一次运行。
type RunCompletedEvent = agent.RunCompletedEvent

// RunFailedEvent 表示 Runtime 因非取消错误结束。
type RunFailedEvent = agent.RunFailedEvent

// RunCanceledEvent 表示 Runtime 因 context 取消或 deadline 结束。
type RunCanceledEvent = agent.RunCanceledEvent

// ContextSummaryCandidateEvent 携带仅供 Session 成功提交的摘要正文。
type ContextSummaryCandidateEvent = agent.ContextSummaryCandidateEvent

// MemoryCandidateEvent 表示 Runtime 提出的长期记忆候选；仅成功终态后可提交。
type MemoryCandidateEvent = agent.MemoryCandidateEvent

// MemoryExtractionFailedEvent 表示候选提取失败，不改变主 Run 终态。
type MemoryExtractionFailedEvent = agent.MemoryExtractionFailedEvent

// RunStartEvent 表示 Agent 已开始一次运行。
type RunStartEvent = agent.RunStartEvent

// TurnStartEvent 表示 Agent 已开始处理当前用户 turn。
type TurnStartEvent = agent.TurnStartEvent

// MessageStartEvent 表示一条 assistant message 开始生成。
type MessageStartEvent = agent.MessageStartEvent

// MessageDeltaEvent 表示一条 assistant message 的增量内容。
type MessageDeltaEvent = agent.MessageDeltaEvent

// MessageDeltaKind 标识一段 message 增量属于可见文本、thinking 还是 tool call 参数。
type MessageDeltaKind = agent.MessageDeltaKind

const (
	// MessageDeltaText 表示 assistant 可见文本增量。
	MessageDeltaText = agent.MessageDeltaText
	// MessageDeltaThinking 表示模型 reasoning/thinking 增量。
	MessageDeltaThinking = agent.MessageDeltaThinking
	// MessageDeltaToolCall 表示 assistant tool call 参数增量。
	MessageDeltaToolCall = agent.MessageDeltaToolCall
)

// MessageEndEvent 表示一条 assistant message 已完整生成。
type MessageEndEvent = agent.MessageEndEvent

// ToolExecutionStartEvent 表示本地工具开始执行。
type ToolExecutionStartEvent = agent.ToolExecutionStartEvent

// ToolExecutionEndEvent 表示本地工具已返回结果。
type ToolExecutionEndEvent = agent.ToolExecutionEndEvent

// ToolResultStatus 是 tool call 的稳定终态。
type ToolResultStatus = agent.ToolResultStatus

const (
	// ToolResultSuccess 表示工具成功执行。
	ToolResultSuccess = agent.ToolResultSuccess
	// ToolResultError 表示参数校验或工具执行失败。
	ToolResultError = agent.ToolResultError
	// ToolResultDenied 表示工具未授权或审批未通过。
	ToolResultDenied = agent.ToolResultDenied
	// ToolResultTimeout 表示工具超时。
	ToolResultTimeout = agent.ToolResultTimeout
	// ToolResultCanceled 表示工具因 Run 取消而终止。
	ToolResultCanceled = agent.ToolResultCanceled
	// ToolResultSkipped 表示工具未执行但已生成配对结果。
	ToolResultSkipped = agent.ToolResultSkipped
)

// ToolApprovalRequestedEvent 表示 Runtime 已请求工具审批。
type ToolApprovalRequestedEvent = agent.ToolApprovalRequestedEvent

// ToolApprovalDecidedEvent 表示工具审批已完成。
type ToolApprovalDecidedEvent = agent.ToolApprovalDecidedEvent

// TurnEndEvent 表示当前用户 turn 已结束。
type TurnEndEvent = agent.TurnEndEvent

// RunEndEvent 表示 Agent 已成功结束一次运行。
type RunEndEvent = agent.RunEndEvent

// ErrorEvent 表示 Agent 运行因错误结束。
type ErrorEvent = agent.ErrorEvent
