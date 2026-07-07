# LLMS + Agent 流式响应设计

## 背景

当前 `internal/agent` 已经对外暴露事件流，但 `internal/llms.Provider` 只有阻塞式 `Chat`。因此 Agent 的 `MessageDeltaEvent` 只是把完整 `ChatResponse` 投影成一次或少数几次 delta，不会随上游 SSE 实时输出。

本阶段目标是在 `llms` 层补真正的上游 streaming，并让 `agent` 优先消费 streaming provider；`session` 和 CLI 继续复用现有事件流，不新增 HTTP、database、memory、Responses API。

## 目标

- `internal/llms` 支持 OpenAI-compatible `/chat/completions` SSE 流式响应。
- `internal/agent` 在 provider 支持 streaming 时实时透出文本、thinking 和 tool call 参数 delta。
- 保留现有阻塞式 `Chat` 行为作为兼容 fallback。
- fake provider 提供确定性 streaming，用于无网络测试 tool-calling。
- cancellation、上游错误、畸形 SSE 都产生可观察错误，不静默关闭。

## 非目标

- 不实现 HTTP API 或浏览器端实时输出。
- 不引入持久化、memory、resume、branch。
- 不接 OpenAI Responses API。
- 不改变工具执行协议和工具 schema 暴露方式。
- 不移除现有 `Provider.Chat`。

## 接口设计

现有接口保持不变：

```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}
```

新增可选 streaming 接口：

```go
type StreamingProvider interface {
    ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error)
}
```

`ChatStreamEvent` 是 provider 层事件，表示单条 assistant message 的生成过程。事件集合：

- `ChatStreamStartEvent`：assistant message 开始。
- `ChatStreamTextDeltaEvent`：可见文本增量。
- `ChatStreamThinkingDeltaEvent`：reasoning/thinking 增量。
- `ChatStreamToolCallDeltaEvent`：tool call 的 id、type、name 或 arguments 增量。
- `ChatStreamEndEvent`：完整 assistant message，可直接用于后续 tool-calling 判断。
- `ChatStreamErrorEvent`：上游错误、解析错误或 context cancellation。

最终完整内容仍聚合为现有 `llms.Message`，避免新增并行 message 模型。

## OpenAI-compatible SSE 适配

`OpenAICompatibleProvider.ChatStream` 使用同一个 `/chat/completions` endpoint，请求体在现有 payload 基础上增加 `stream: true`。

解析规则：

- 只处理 `data:` 行，忽略空行和注释行。
- `data: [DONE]` 表示正常结束。
- 每个 JSON chunk 读取 `choices[0].delta`。
- `delta.content` 生成 `ChatStreamTextDeltaEvent`。
- `delta.reasoning_content` 生成 `ChatStreamThinkingDeltaEvent`。
- `delta.tool_calls[]` 按 `index` 聚合：
  - `id`、`type`、`function.name` 更新 tool call metadata。
  - `function.arguments` 追加到对应 tool call 的 arguments。
- 缺失 tool call `type` 时默认 `function`，与非流式路径保持一致。
- 非 2xx 响应读取有限 error body 并返回错误。
- stream 正常结束但没有完整 assistant message 时返回错误事件。

## Agent 集成

`internal/agent` 在每轮 LLM 调用时先检测 provider：

```go
if streamer, ok := provider.(llms.StreamingProvider); ok {
    // consume ChatStream
} else {
    // call Chat
}
```

streaming 路径把 provider 事件映射到现有 agent 事件：

- text delta -> `MessageDeltaEvent{Kind: MessageDeltaText}`
- thinking delta -> `MessageDeltaEvent{Kind: MessageDeltaThinking}`
- tool call delta -> `MessageDeltaEvent{Kind: MessageDeltaToolCall}`
- stream end -> `MessageEndEvent`
- provider error/cancel -> `ErrorEvent`

Agent 主循环仍只在完整 assistant message 后判断 tool calls、执行工具、追加 tool result 并进入下一轮。

## Fake Provider

`FakeProvider.ChatStream` 提供确定性事件：

- 第一轮输出 calculator tool call metadata 和 arguments delta，最终 message 含完整 tool call。
- 第二轮把最终文本分成至少两个 text delta，最终 message 含完整 content。

测试不得依赖真实网络或付费 API。

## 错误与取消

- `ChatStream` 返回前发生同步错误时直接返回 `error`。
- stream goroutine 内发生错误时发送 `ChatStreamErrorEvent` 后关闭 channel。
- context cancellation 视为可观察错误，不允许静默关闭。
- Agent 收到 error event 后发 `ErrorEvent` 并停止当前 run。
- 已产生的 transcript 只包含已经完成的消息；半截 assistant message 不写入最终历史。

## 测试计划

- `llms`：OpenAI-compatible streaming 请求体包含 `stream: true`。
- `llms`：SSE text delta 能聚合成最终 assistant content。
- `llms`：SSE tool call arguments delta 能按 index 聚合成最终 tool call。
- `llms`：畸形 JSON、非 2xx、空 stream、context cancellation 都返回明确错误。
- `llms`：fake provider streaming 两轮 tool-calling 行为确定。
- `agent`：实现 `StreamingProvider` 时优先走 `ChatStream`，实时透出多个 delta。
- `agent`：非 streaming provider 仍走 `Chat` fallback。
- `agent`：stream cancellation 和 provider error 都映射为 `ErrorEvent`。

## 完成条件

- `go test ./internal/llms ./internal/agent` 通过。
- `go test ./...` 通过。
- 没有新增并行 `internal/ai`、HTTP 路由、database、memory 或 Responses API 代码。
