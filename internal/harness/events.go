package harness

import "harukizmoe/pimoe/internal/agent"

// Event 描述 Harness 对外转发的 Agent 运行事件。
type Event = agent.Event

// RunStartEvent 表示 Agent 已开始一次运行。
type RunStartEvent = agent.RunStartEvent

// LLMRequestEvent 表示 Agent 即将向 Provider 发起一次聊天请求。
type LLMRequestEvent = agent.LLMRequestEvent

// LLMErrorEvent 表示 Provider 聊天请求返回错误。
type LLMErrorEvent = agent.LLMErrorEvent

// ToolCallEvent 表示模型已请求执行本地工具。
type ToolCallEvent = agent.ToolCallEvent

// ToolResultEvent 表示本地工具已返回结果。
type ToolResultEvent = agent.ToolResultEvent

// FinalEvent 表示 Agent 已得到最终回答。
type FinalEvent = agent.FinalEvent

// RunEndEvent 表示 Agent 已成功结束一次运行。
type RunEndEvent = agent.RunEndEvent

// ErrorEvent 表示 Agent 运行因错误结束。
type ErrorEvent = agent.ErrorEvent
