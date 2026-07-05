package agent

// EventType 表示 Agent 运行过程中可暴露的轻量事件类别。
type EventType string

const (
	// EventToolCall 表示模型已请求执行本地工具。
	EventToolCall EventType = "tool_call"
	// EventToolResult 表示本地工具已返回结果。
	EventToolResult EventType = "tool_result"
	// EventFinal 表示 Agent 已得到最终回答。
	EventFinal EventType = "final"
)

// Event 描述一个可供上层消费的轻量运行事件。
type Event struct {
	// Type 标识当前事件类别。
	Type EventType
	// Message 保存事件的简要文本描述。
	Message string
}
