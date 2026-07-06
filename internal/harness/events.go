package harness

import "harukizmoe/pimoe/internal/agent"

// EventType 表示 Harness 对外暴露的 Agent 运行事件类别。
type EventType = agent.EventType

// Event 描述 Harness 对外转发的 Agent 运行事件。
type Event = agent.Event

// EventHandler 接收 Harness 转发的 Agent 运行事件。
type EventHandler = agent.EventHandler

const (
	// EventRunStart 表示 Agent 已开始一次运行。
	EventRunStart = agent.EventRunStart
	// EventLLMRequest 表示 Agent 即将向 Provider 发起一次聊天请求。
	EventLLMRequest = agent.EventLLMRequest
	// EventLLMError 表示 Provider 聊天请求返回错误。
	EventLLMError = agent.EventLLMError
	// EventToolCall 表示模型已请求执行本地工具。
	EventToolCall = agent.EventToolCall
	// EventToolResult 表示本地工具已返回结果。
	EventToolResult = agent.EventToolResult
	// EventFinal 表示 Agent 已得到最终回答。
	EventFinal = agent.EventFinal
	// EventRunEnd 表示 Agent 已成功结束一次运行。
	EventRunEnd = agent.EventRunEnd
	// EventAgentError 表示 Agent 运行因错误结束。
	EventAgentError = agent.EventAgentError
)
