# LLM + Agent 流式输出执行计划

> **给执行代理:** 必须先使用 `superpowers:using-git-worktrees`，再按任务逐项执行。实现阶段推荐使用 `superpowers:subagent-driven-development`；如不拆分子代理，则使用 `superpowers:executing-plans`。所有步骤用 checkbox 跟踪。

**目标:** 给当前 Go 版 `internal/llms` 和 `internal/agent` 加入最小可用 streaming：OpenAI-compatible Provider 使用 `/chat/completions` SSE；Agent 优先消费 streaming provider，并继续保持现有 Chat fallback、tool calling、事件生命周期和 session API 不变。

**非目标:** 不新增 HTTP API、数据库、memory、resume、branch、Responses API；不引入第三方 SSE 依赖；不改变 `internal/session` 对外事件别名；不把业务逻辑放进 provider。

**当前事实:**
- baseline 已通过：`go test ./internal/llms`、`go test ./internal/agent`、`go test ./...`。
- 当前 checkout 是普通 `dev/ai`，不是 worktree；`.worktrees/` 已在 `.gitignore` 中忽略。
- `internal/agent.Stream` 当前只调用 `llms.Provider.Chat`，然后一次性发 `MessageDeltaEvent`。
- `internal/llms.OpenAICompatibleProvider` 当前只支持非流式 JSON 响应。

---

## 执行前隔离

- [ ] 在仓库根目录确认是否已在隔离工作区；若仍是普通 checkout，则创建并进入：
  ```bash
  git worktree add .worktrees/llms-agent-streaming -b feature/llms-agent-streaming dev/ai
  ```
- [ ] 在执行工作区运行基线：`go test ./internal/llms ./internal/agent`。

---

## 任务 1：锁定 `llms` streaming 类型契约

**文件:** `internal/llms/type.go`、`internal/llms/provider.go`

- [ ] 在 `internal/llms/type.go` 增加最小 streaming 事件模型：
  - `ChatStreamEventType`：`delta`、`done`、`error`。
  - `ChatStreamEvent`：包含 `Type ChatStreamEventType`、`Delta ChatStreamDelta`、`Message Message`、`Error error`。
  - `ChatStreamDelta`：包含 `Role Role`、`Content string`、`ReasoningContent string`、`ToolCalls []ToolCallDelta`。
  - `ToolCallDelta`：包含 `Index int`、`ID string`、`Type string`、`Function ToolCallFunction`。
- [ ] 在 `internal/llms/provider.go` 增加接口：
  ```go
  // StreamingProvider 表示支持增量返回 assistant 消息的 Provider。
  type StreamingProvider interface {
      Provider
      ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error)
  }
  ```
- [ ] 保持 `Provider` 只要求 `Chat`，保证现有 provider 和测试不用改。

**验收:** `go test ./internal/llms` 编译通过；未实现 `ChatStream` 的 provider 仍满足 `Provider`。

---

## 任务 2：先补 `llms` streaming RED 测试

**文件:** 修改 `internal/llms/openai_compatible_test.go` 和 `internal/llms/fake_test.go`，优先复用现有 helper 风格。

- [ ] 新增 `TestOpenAICompatibleProviderChatStreamSendsStreamingPayloadAndParsesText`：
  - `httptest.Server` 断言请求路径是 `/v1/chat/completions`、认证头和 `Content-Type` 正确。
  - 解码请求体，断言 `stream: true`、`model`、`messages` 保持现有 Chat 编码语义。
  - 返回 SSE：role chunk、`content:"hel"`、`content:"lo"`、`finish_reason:"stop"`、`data: [DONE]`。
  - `collectChatStreamEvents` 断言两个 text delta 顺序为 `hel`、`lo`，最终 done message 为 `RoleAssistant` + `Content:"hello"`，且无 error event。
- [ ] 新增 `TestOpenAICompatibleProviderChatStreamAggregatesToolCallChunks`：
  - 用 `writeSSE` 写入同一 `index` 的 tool-call chunk；第一段带 `id/name`，后续只带 `function.arguments` 片段。
  - 至少一个 chunk 省略 `type`，最终必须归一化为 `function`。
  - 断言最终 done message 只有 1 个 tool call：`ID:"call_1"`、`Type:"function"`、`Function.Name:"calculator"`、`Function.Arguments` 为完整 JSON。
- [ ] 新增 `TestOpenAICompatibleProviderChatStreamSurfacesStatusAndMalformedStreamErrors`：
  - 表驱动覆盖 `status_error`：服务端返回 500 + `upstream exploded`，`ChatStream` 同步返回包含 body 摘要的 error。
  - 覆盖 `malformed_chunk`：服务端返回非法 JSON SSE frame，drain stream 后出现 error event，且没有 done event。
  - malformed error 文案包含 decode/unmarshal 上下文。
- [ ] 新增 `TestOpenAICompatibleProviderChatStreamReturnsErrorWhenStreamEndsWithoutDone`：
  - 服务端返回一个非终止 chunk 后关闭连接。
  - 断言 error event 包含 `ended without done`，避免静默成功。
- [ ] 新增 `TestFakeProviderChatStreamReturnsToolCallThenFinalAnswer`：
  - 第一轮无 tool result 时，stream done 中返回 calculator tool call。
  - 第二轮带先前 assistant tool-call message + `RoleTool` 结果 `91` 时，delta 文本拼接为 `13 * 7 = 91`，done message content 相同。

**验收:** 这些测试先失败在缺少类型/方法或未实现行为上；实现后通过。

---

## 任务 3：实现 OpenAI-compatible SSE streaming

**文件:** `internal/llms/openai_compatible.go`

- [ ] 给 `openAIChatRequest` 增加 `Stream bool \`json:"stream,omitempty"\``，只在 `ChatStream` 设置为 `true`，不要影响 `Chat`。
- [ ] 增加 SSE chunk 私有结构：
  - `openAIChatStreamChunk{Choices []openAIStreamChoice, Error *openAIStreamError}`。
  - `openAIStreamChoice{Delta openAIStreamDelta, FinishReason string}`。
  - `openAIStreamDelta{Role, Content, ReasoningContent string, ToolCalls []openAIStreamToolCallDelta}`，并兼容 `reasoning_content`。
  - `openAIStreamToolCallDelta{Index int, ID, Type string, Function openAIToolCallFunction}`。
- [ ] 实现 `ChatStream(ctx, req)`：
  - marshal 与 `Chat` 相同的 payload，但 `stream: true`。
  - 复用 `Authorization`、`Content-Type`、非 2xx 错误体截断逻辑。
  - 请求创建或非 2xx 这类同步失败直接 `return nil, error`。
  - 2xx 后返回 channel，并在 goroutine 中解析 `resp.Body`；goroutine 负责 `defer resp.Body.Close()`。
- [ ] SSE 解析规则：
  - 使用 `bufio.Reader.ReadString('\n')`，不要用默认 `bufio.Scanner`，避免 64 KiB token 限制。
  - 支持多行 `data:` 合并；空行表示一个 SSE event 完成。
  - 忽略注释行和 `event:` 行。
  - `data: [DONE]` 后若还没发送 done，则用已聚合消息发送 done 并结束。
  - JSON `{ "error": { "message": ... } }` 转为 error event。
  - usage-only chunk（`choices` 为空）忽略。
- [ ] 聚合规则：
  - `delta.role` 非空时更新最终 message role；默认最终 role 为 `assistant`。
  - `delta.content` 追加到最终 `Message.Content`，同时发送 `delta` 事件。
  - `delta.reasoning_content` 只发送 `Delta.ReasoningContent`，当前最终 `Message` 不持久化 thinking。
  - `delta.tool_calls` 按 `index` 聚合；缺失 `type` 归一化为 `function`；`id/name` 允许在后续 chunk 补齐；`function.arguments` 按字符串片段追加。
  - 每个非空参数片段都发送 delta event，最终 `done.Message.ToolCalls` 使用聚合结果。
- [ ] 终止规则：
  - 收到 `finish_reason` 或 `[DONE]` 后发送一次 done 并结束。
  - body EOF 但未 done：发送 error event `openai chat stream ended without done`。
  - JSON decode、读取错误、`ctx.Err()`：发送 error event；不要 panic。

**验收:** `go test ./internal/llms` 通过。

---

## 任务 4：实现 fake provider streaming

**文件:** `internal/llms/fake.go`

- [ ] 给 `FakeProvider` 增加 `ChatStream(ctx, req)`。
- [ ] 行为必须复用 `Chat(ctx, req)` 的确定性结果，不分叉业务判断：
  - 先调用 `p.Chat(ctx, req)` 得到最终标准化 message。
  - 若 context 已取消，返回 error event 或同步错误。
  - 若 message 有 `Content`，至少发送一个 text delta，再发送 done。
  - 若 message 有 tool call，发送对应 tool call argument delta，再发送 done。
- [ ] 不引入 sleep，不依赖真实时间。

**验收:** fake stream 测试和既有 fake/tool calling 测试通过。

---

## 任务 5：让 Agent 优先使用 streaming provider

**文件:** `internal/agent/loop.go`

- [ ] 保留当前 `Chat` fallback 路径；未实现 `llms.StreamingProvider` 的 provider 行为不变。
- [ ] 在每个 chat round 中：
  - 如果 `a.provider` 实现 `llms.StreamingProvider`，调用 `ChatStream`。
  - 否则继续调用 `Chat` 并用现有 `emitAssistantLifecycle` 一次性发 delta/end。
- [ ] 新增私有 helper，避免 `stream` 主循环膨胀：
  - `runChatRound(ctx, emit, runID, messageID, chatRound, req) (AssistantMessage, error)` 或等价命名。
  - `streamAssistantMessage(...)` 负责消费 `ChatStreamEvent`、发 `MessageStartEvent`、增量 `MessageDeltaEvent`、最终 `MessageEndEvent`。
  - `chatAssistantMessage(...)` 包装现有 `Chat` + `emitAssistantLifecycle`。
- [ ] streaming 消费规则：
  - `MessageStartEvent` 在收到 stream channel 后立即发出。
  - `ChatStreamEventTypeDelta`：
    - `Delta.Content` -> `MessageDeltaText`，`ContentIndex: 0`。
    - `Delta.ReasoningContent` -> `MessageDeltaThinking`，`ContentIndex: 0`。
    - `Delta.ToolCalls[i].Function.Arguments` -> `MessageDeltaToolCall`，`ContentIndex` 使用 tool call `Index`。
  - `ChatStreamEventTypeDone`：验证最终 assistant message 有 content 或 tool calls，然后发 `MessageEndEvent`。
  - `ChatStreamEventTypeError` 或 channel 关闭前无 done：返回错误，由主循环转成 `ErrorEvent`。
- [ ] 保持 tool calling 逻辑位置不变：`maxSteps`、`ToolExecutionStartEvent`、`ToolExecutionEndEvent`、tool result 回填都继续基于最终 `AssistantMessage`。
- [ ] 错误文案沿用现有 chat round 语义，例如 `llm chat round 1: ...`；context cancellation 仍走 `emitCancellation`。

**验收:** 既有 `internal/agent` 测试不破；新增 streaming agent 测试通过。

---

## 任务 6：补 Agent streaming RED/回归测试

**文件:** 修改 `internal/agent/loop_test.go` 或新建 `internal/agent/streaming_provider_test.go`

- [ ] 增加 helper `streamingProviderStub`：同时实现 `Chat` 和 `ChatStream`，记录 `chatCalls`、`streamCalls` 和请求。
- [ ] 新增 `TestAgentStreamUsesStreamingProviderWhenAvailable`：
  - `Chat` 若被调用则计数并让测试失败；`ChatStream` 发送 `streamed `、`answer` 两个 text delta 和 done message。
  - 断言 `streamCalls == 1`、`chatCalls == 0`，请求消息可用现有 `assertMessagesEqual` 校验。
  - 断言事件中两个 `MessageDeltaEvent` 在 `MessageEndEvent` 前，最终 content 为 `streamed answer`。
- [ ] fallback 覆盖保留现有 `TestAgentStreamEmitsNoToolMessageLifecycle` / Chat-only provider 路径；除非实现破坏该测试，不新增重复用例。
- [ ] 新增 `TestAgentStreamStreamingToolCallsContinueWithToolResult`：
  - 第一轮 stream 返回 calculator tool call 参数分片和 done tool call。
  - Agent 执行 calculator 后第二轮 stream 返回最终文本。
  - 断言 tool start/end 仍出现，最终答案为 `13 * 7 = 91`。
- [ ] 新增 `TestAgentStreamReturnsStreamingProviderErrorWithoutChatFallback`：
  - `ChatStream` 同步返回 sentinel error 或 stream 发 error event；`Chat` 被调用即失败。
  - 断言输出含 `ErrorEvent`，error 包含 `llm chat round 1` 和 sentinel，且没有 `RunEndEvent`。

**验收:** `go test ./internal/agent` 通过。

---

## 任务 7：全量验证与清理

- [ ] 运行：`gofmt -w internal/llms internal/agent`。
- [ ] 运行：`go test ./internal/llms ./internal/agent`。
- [ ] 运行：`go test ./...`。
- [ ] 检查是否需要更新 `docs/evolution/lessons.md`：只有当实现中发现可复用的 streaming/SSE 边界经验时才记录；不要写一次性流水账。
- [ ] 确认未改动 `internal/session` API；如编译要求别名新增类型，仅做最小 alias。
- [ ] 最终报告列出：改动文件、验证命令、未覆盖风险；没有风险写“无”。
