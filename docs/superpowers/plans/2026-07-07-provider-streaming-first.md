# Provider Streaming-First 重构执行计划

> **给执行代理:** 必须先使用 `superpowers:using-git-worktrees`；按任务顺序执行，保持 TDD：先让对应测试失败，再实现。若使用子代理，必须用 `superpowers:subagent-driven-development`；若单人执行，必须用 `superpowers:executing-plans`。所有步骤用 checkbox 跟踪。

**目标:** 将 `internal/llms.Provider` 改成 streaming-first：只暴露 `ChatStream(ctx, req)`；删除 `StreamingProvider` 抽象、Provider 上的 `Chat` 方法和 Agent 的 Chat fallback；同步场景统一通过 `llms.CollectChat` 从 stream 收集最终消息。

**非目标:** 不新增 Anthropic provider、HTTP API、memory、database、Responses API；不重写 OpenAI-compatible SSE 解析器；不保留 `StreamingProvider` alias/shim；不改变 `internal/session` 对外 API。

**当前事实:**
- 已批准设计：`docs/superpowers/specs/2026-07-07-provider-streaming-first-design.md`。
- 当前基线通过：`go test ./internal/llms ./internal/agent`。
- 当前 checkout：`dev/ai`，工作区 clean。
- 当前 `Provider` 仍要求 `Chat`，`StreamingProvider` 仍存在。
- 当前 Agent 仍通过 `provider.(llms.StreamingProvider)` 选择 streaming，并保留 `Chat` fallback。

---

## 执行前隔离

- [ ] 在仓库根目录确认是否已在隔离工作区；若仍是普通 checkout，则创建并进入：
  ```bash
  git worktree add .worktrees/provider-streaming-first -b feature/provider-streaming-first dev/ai
  ```
- [ ] 在执行工作区运行基线：
  ```bash
  go test ./internal/llms ./internal/agent
  ```

---

## 任务 1：RED 锁定新 Provider 契约和 CollectChat 行为

**文件:** `internal/llms/streaming_contract_test.go`、新建 `internal/llms/collect_chat_test.go`

- [ ] 改写 `streaming_contract_test.go`：
  - 删除 `streamingProviderStub.Chat`。
  - 删除 `var _ StreamingProvider = ...`。
  - 新增或改为：
    ```go
    type streamingProviderStub struct{}

    func (streamingProviderStub) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error) {
        return nil, nil
    }

    var _ Provider = streamingProviderStub{}
    ```
  - 保留现有 `ChatStreamEvent` delta/done/error 结构测试。
- [ ] 新增 `collect_chat_test.go`，覆盖 `CollectChat` 的四个同步视图语义：
  - stream setup error：`provider.ChatStream` 直接返回 error，`CollectChat` 原样返回带上下文的 error。
  - done event：返回 `*ChatResponse{Message: done.Message}`。
  - error event：返回 event error，不继续等待 done。
  - stream 关闭但没有 done：返回包含 `ended without done` 或等价语义的错误。
- [ ] 先运行 RED：
  ```bash
  go test ./internal/llms -run 'TestStreamingContract|TestCollectChat'
  ```
  预期失败点：`Provider` 仍要求 `Chat`，且 `CollectChat` 尚不存在。

**验收:** RED 失败原因必须对应新契约/新 helper，不能是测试拼写或导入错误。

---

## 任务 2：GREEN 改造 `llms.Provider` 并实现 `CollectChat`

**文件:** `internal/llms/provider.go`、新建 `internal/llms/collect_chat.go`、`internal/llms/*_test.go`

- [ ] 在 `provider.go` 中把 `Provider` 改成唯一接口：
  ```go
  type Provider interface {
      ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error)
  }
  ```
- [ ] 删除 `StreamingProvider` 类型定义。不要新增 alias、shim 或 deprecated 类型。
- [ ] 保持 `Factory`、`Registry`、`NewProvider` 的签名和注册模型不变。
- [ ] 新增 `CollectChat(ctx, provider, req)`：
  - 调用 `provider.ChatStream(ctx, req)`。
  - 同步 setup error 直接返回。
  - 遇到 `ChatStreamEventTypeDone` 返回 `ChatResponse`。
  - 遇到 `ChatStreamEventTypeError` 返回 event error；event error 为 nil 时返回明确错误。
  - channel 关闭前没有 done 时返回明确错误。
  - 不把 delta 当最终事实；delta 最多用于后续调试，不参与返回 message。
- [ ] 删除或替换测试 helper `requireStreamingProvider`：Provider 本身已经有 `ChatStream`。
- [ ] 更新 `internal/llms/fake_test.go`：同步 fake 测试先改为 `CollectChat(ctx, provider, req)`，不要调用 `provider.Chat`。
- [ ] 更新 `internal/llms/openai_compatible_test.go` 中的 streaming 测试调用：
  ```go
  stream, err := provider.ChatStream(ctx, req)
  ```
  不再经由 `requireStreamingProvider`。
- [ ] 运行：
  ```bash
  go test ./internal/llms -run 'TestStreamingContract|TestCollectChat|TestFakeProvider'
  ```

**验收:** `llms.Provider` 编译契约已是 streaming-only；`StreamingProvider` 类型定义不存在；focused llms 测试通过。

---

## 任务 3：删除 `FakeProvider.Chat`，保留确定性 stream 行为

**文件:** `internal/llms/fake.go`、`internal/llms/fake_test.go`

- [ ] 把原 `FakeProvider.Chat` 的业务判断抽成私有函数，例如：
  ```go
  func (p *FakeProvider) chatMessage(req ChatRequest) Message
  ```
  或等价命名。该函数只返回标准化 assistant message，不暴露为 provider 方法。
- [ ] 删除 `func (p *FakeProvider) Chat(...)`。
- [ ] `ChatStream` 调用私有函数生成最终消息：
  - 有 tool call：发送 tool call delta，再发送 done。
  - 有 content：发送 text delta，再发送 done。
  - context 已取消：返回 error event 或同步错误，沿用当前最小实现即可。
- [ ] `TestFakeProviderReturnsToolCallThenFinalAnswer` 必须通过 `CollectChat` 验证两轮同步视图。
- [ ] `TestFakeProviderChatStreamReturnsToolCallThenFinalAnswer` 继续验证 delta + done。
- [ ] 运行：
  ```bash
  go test ./internal/llms -run TestFakeProvider
  ```

**验收:** `grep` 不再能在 `fake.go` 找到 `func (p *FakeProvider) Chat(`；fake provider 仍保持确定性 tool-call 闭环。

---

## 任务 4：迁移 OpenAI-compatible 同步测试并删除 `OpenAICompatibleProvider.Chat`

**文件:** `internal/llms/openai_compatible.go`、`internal/llms/openai_compatible_test.go`

- [ ] 删除 `func (p *OpenAICompatibleProvider) Chat(...)`。
- [ ] 若 `doChatCompletions` 仍带 `stream bool` 参数，简化为只服务 streaming；请求体必须始终带 `stream: true`。
- [ ] 迁移旧非流式 JSON 测试，不保留第二协议路径：
  - `TestOpenAICompatibleProviderSendsChatCompletionPayload`：改为 SSE 响应；继续断言 model、messages、tools、tool message、authorization、content-type、`stream:true`；同步最终结果用 `CollectChat` 或直接 drain `ChatStream`。
  - `TestOpenAICompatibleProviderPreservesEmptyToolContent`：改为 `ChatStream` 请求后 drain stream；继续断言 payload 包含空 tool content 字段。
  - `TestOpenAICompatibleProviderStatusErrorIncludesBodyExcerpt`：若 streaming 表驱动已覆盖 5xx + body excerpt，则删除旧重复测试；否则改为 `ChatStream` 同步 error。
  - `TestOpenAICompatibleProviderReturnsDecodeErrorOnMalformedJSON`：删除旧非流式 JSON 版本；保留现有 malformed SSE frame 测试。
  - `TestOpenAICompatibleProviderNormalizesMissingToolCallTypeToFunction`：改为 SSE tool-call chunk + `CollectChat`，断言最终 tool call type 为 `function`。
  - `TestOpenAICompatibleProviderReturnsErrorOnEmptyChoices`：不要按非流式 `choices:[]` 语义保留；streaming 中 usage-only chunk 应继续被忽略，由现有 `IgnoresUsageOnlyChunks` 覆盖。
- [ ] 确认现有 SSE 回归仍覆盖：
  - request `stream:true`。
  - usage-only chunk。
  - tool call 分片聚合。
  - status error。
  - malformed frame。
  - error frame。
  - EOF without done。
  - `finish_reason` without `[DONE]`。
- [ ] 运行：
  ```bash
  go test ./internal/llms -run 'TestOpenAICompatibleProvider|TestCollectChat'
  ```

**验收:** OpenAI-compatible provider 只有 `ChatStream` provider 方法；所有 OpenAI 测试只走 SSE/streaming 语义。

---

## 任务 5：让 Agent 删除 Chat fallback，只走 `ChatStream`

**文件:** `internal/agent/loop.go`、`internal/agent/loop_test.go`、`internal/agent/event_lifecycle_test.go`、`internal/agent/streaming_provider_test.go`

- [ ] 在 `loop.go` 中删除 runtime type assertion：
  ```go
  provider, ok := a.provider.(llms.StreamingProvider)
  ```
- [ ] 删除 `chatAssistantMessage` 和 `emitAssistantLifecycle` 中只服务 Chat fallback 的路径；如果 `emitAssistantLifecycle` 已无调用，连同测试无用 helper 一起删除。
- [ ] `runChatRound` 直接调用：
  ```go
  assistantMessage, err := streamAssistantMessage(ctx, emit, a.provider, runID, messageID, chatRound, a.maxSteps, req)
  ```
  返回值不再需要 `streamed bool` 或等价分支标记。
- [ ] 保留 streaming 消费规则：
  - `MessageStartEvent` 在 stream 建立后立即发出。
  - `Delta.Content` -> `MessageDeltaText`。
  - `Delta.ReasoningContent` -> `MessageDeltaThinking`。
  - `Delta.ToolCalls[].Function.Arguments` -> `MessageDeltaToolCall`。
  - `Done` 前执行 `validateAssistantMessage` 和 max-step 终态拒绝。
  - `Error` event 或 channel 关闭无 done -> 返回错误，由主循环发 `ErrorEvent`。
- [ ] 更新 test helpers：
  - `recordingProvider` 改为记录并转发 `ChatStream`。
  - `chatFunc` 改名为 `streamFunc` 或删除；测试中的 no-tool provider 用 `ChatStream` stub。
  - `streamingProviderStub` 删除 `Chat`、`chatCalls`、`chatFunc` 字段；保留 `streamCalls` 和 requests。
- [ ] 删除“优先使用 streaming provider / 不 fallback 到 Chat”的断言中对 `chatCalls == 0` 的依赖；现在接口已经无法调用 `Chat`，改断言 `streamCalls` 和事件顺序即可。
- [ ] `TestAgentStreamReturnsStreamingProviderErrorWithoutChatFallback` 改名为更准确的 `TestAgentStreamReturnsProviderStreamError` 或等价名称；不再构造 Chat fallback。
- [ ] 更新 `event_lifecycle_test.go` 的 no-tool provider：用 `ChatStream` 发送 text delta + done。
- [ ] 运行：
  ```bash
  go test ./internal/agent -run 'TestAgentStream|TestAgent'
  ```

**验收:** Agent 核心路径不存在 Chat fallback；事件生命周期、tool calling、stream error、max steps 行为不退化。

---

## 任务 6：迁移 `internal/session` 测试 stub，不改 session API

**文件:** `internal/session/session_test.go`

- [ ] 把 `maxStepLoopProvider.Chat` 改为 `ChatStream`：
  - 第一轮返回 tool-call done。
  - 第二轮校验上一条 tool result 后返回下一次 tool-call done。
  - 超出轮次仍 `t.Fatalf`。
- [ ] 把 `blockingProvider.Chat` 改为 `ChatStream`：
  - 进入方法时关闭 `started`。
  - 等待 `release` 后返回包含 text done 的 stream。
  - context done 时返回 error event 或同步 `ctx.Err()`，保持原 cancellation 语义。
- [ ] 不改 `internal/session` 包的对外类型别名和调用方式。
- [ ] 运行：
  ```bash
  go test ./internal/session
  ```

**验收:** session API 不变；session 测试只通过 streaming provider stub 驱动。

---

## 任务 7：全仓迁移检查与最小清理

**文件:** 视 grep 结果而定；只删无用代码，不做额外重构。

- [ ] 用内置 grep 检查并迁移剩余引用：
  - `StreamingProvider`
  - `.Chat(`
  - `func (.*) Chat(`
  - `chatAssistantMessage`
  - `chatFunc`
  - `requireStreamingProvider`
- [ ] 合理例外：
  - 类型名 `ChatRequest`、`ChatResponse`、`ChatStreamEvent` 保留。
  - `CollectChat` 内部调用 `ChatStream`，不算旧 Chat provider 路径。
- [ ] 删除不再使用的 imports、helpers、测试字段。
- [ ] 运行 gofmt：
  ```bash
  gofmt -w internal/llms internal/agent internal/session
  ```

**验收:** 没有旧抽象、旧 fallback、旧 provider `Chat` 方法；只留下 streaming-first API 和 `CollectChat` convenience helper。

---

## 任务 8：最终验证

- [ ] 运行 focused 测试：
  ```bash
  go test ./internal/llms ./internal/agent ./internal/session
  ```
- [ ] 运行全量测试：
  ```bash
  go test ./...
  ```
- [ ] 用内置 grep 记录最终证据：
  - `StreamingProvider` 无类型定义、无 runtime assertion。
  - provider 上没有 `Chat(ctx context.Context, req ChatRequest)` 方法。
  - Agent 不再有 `chatAssistantMessage` 或 Chat fallback。
- [ ] 检查工作区：
  ```bash
  git status --short --untracked-files=all
  ```
- [ ] 最终报告列出：改动文件、验证命令、grep 证据、遗留风险；没有风险写“无”。

**完成定义:** `go test ./...` 通过；`llms.Provider` 只要求 `ChatStream`；`StreamingProvider` 不存在；OpenAI/Fake/Agent/session 全部迁移到 streaming-first；未留下 shim、alias 或旧 fallback。
