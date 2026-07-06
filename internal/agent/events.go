package agent

// EventType 表示 Agent 运行过程中可暴露的轻量事件类别。
type EventType string

const (
	// EventRunStart 表示 Agent 已开始一次运行。
	EventRunStart EventType = "run_start"
	// EventLLMRequest 表示 Agent 即将向 Provider 发起一次聊天请求。
	EventLLMRequest EventType = "llm_request"
	// EventLLMError 表示 Provider 聊天请求返回错误。
	EventLLMError EventType = "llm_error"
	// EventToolCall 表示模型已请求执行本地工具。
	EventToolCall EventType = "tool_call"
	// EventToolResult 表示本地工具已返回结果。
	EventToolResult EventType = "tool_result"
	// EventFinal 表示 Agent 已得到最终回答。
	EventFinal EventType = "final"
	// EventRunEnd 表示 Agent 已成功结束一次运行。
	EventRunEnd EventType = "run_end"
	// EventAgentError 表示 Agent 运行因错误结束。
	EventAgentError EventType = "agent_error"
)

// Event 描述一个可供上层消费的轻量运行事件。
type Event struct {
	// Type 标识当前事件类别。
	Type EventType
	// Message 保存事件的人类可读摘要；程序逻辑不应依赖该字段。
	Message string
	// ChatRound 保存 LLM 请求轮次；非 LLM 事件为 0。
	ChatRound int
	// ToolName 保存工具事件对应的本地工具名；非工具事件为空。
	ToolName string
	// ToolCallID 保存工具事件对应的模型 tool call ID；非工具事件为空。
	ToolCallID string
	// IsError 标识该事件是否表示失败结果；非失败事件为 false。
	IsError bool
	// Error 保存错误事件的原始错误；非错误事件为空。
	Error error
}
