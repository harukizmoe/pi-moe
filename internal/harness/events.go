package harness

import "harukizmoe/pimoe/internal/agent"

// Event 描述 Harness 对外转发的 Agent 运行事件。
type Event = agent.Event

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

// TurnEndEvent 表示当前用户 turn 已结束。
type TurnEndEvent = agent.TurnEndEvent

// RunEndEvent 表示 Agent 已成功结束一次运行。
type RunEndEvent = agent.RunEndEvent

// ErrorEvent 表示 Agent 运行因错误结束。
type ErrorEvent = agent.ErrorEvent
