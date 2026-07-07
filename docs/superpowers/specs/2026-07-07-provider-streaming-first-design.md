# Provider Streaming-First 重构设计

## 背景

当前 `internal/llms` 同时存在 `Provider` 和 `StreamingProvider`：

- `Provider` 只要求 `Chat(ctx, req)`。
- `StreamingProvider` 额外要求 `ChatStream(ctx, req)`。
- `internal/agent` 通过运行时 type assertion 判断是否支持 streaming，并保留 Chat fallback。

这个边界不够优雅。主流 provider（OpenAI-compatible、Anthropic-compatible）通常都支持流式响应，Agent 的主输出形态也是事件流。继续把 streaming 建模为可选能力，会让核心路径分裂成两套语义。

## 目标

把 streaming 作为 LLM Provider 的唯一底层协议能力：`llms.Provider` 只暴露 `ChatStream`，删除 `StreamingProvider` 抽象和 Agent 的 Chat fallback。

## 非目标

- 不引入 Anthropic provider。
- 不重写 OpenAI-compatible SSE 解析器。
- 不新增 HTTP API、memory、database 或 Responses API。
- 不保留旧 `StreamingProvider` alias；干净切换，迁移所有调用方。

## 设计

### Provider 契约

`internal/llms.Provider` 改为唯一接口：

```go
type Provider interface {
    ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error)
}
```

删除：

```go
type StreamingProvider interface { ... }
```

`Factory` 和 `Registry` 仍返回 `Provider`，不改变配置注册模型。

### 同步 Chat 处理

`Chat` 不再是 Provider 必须实现的方法。若测试或后续业务需要同步最终消息，使用包内 helper 从 stream 收集：

```go
func CollectChat(ctx context.Context, provider Provider, req ChatRequest) (*ChatResponse, error)
```

该 helper 只消费 provider-neutral `ChatStreamEvent`：

- `Delta`：忽略或累积仅用于调试，不作为最终事实来源。
- `Done`：返回最终 `ChatResponse`。
- `Error`：返回错误。
- stream 关闭但没有 `Done`：返回错误。

这样同步路径只是 streaming 的派生视图，不再形成第二套 provider 实现。

### Agent 路径

`internal/agent` 直接调用 `a.provider.ChatStream(ctx, req)`：

- 删除 `provider.(llms.StreamingProvider)` 判断。
- 删除 Chat fallback。
- 删除 `chatAssistantMessage`。
- `MessageStart -> MessageDelta* -> MessageEnd` 成为唯一 assistant 生命周期。
- 保留现有终态校验：`validateAssistantMessage`、max steps 检查必须发生在 `MessageEndEvent` 前。

### Provider 实现

`OpenAICompatibleProvider`：

- 保留 `ChatStream` 作为唯一接口实现。
- 删除挂在 provider 上的 `Chat` 方法；需要同步行为的测试或调用方统一使用 `llms.CollectChat`。
- 保留 `/chat/completions` 的 `stream: true` 请求构造和 SSE 解析行为。
- 保留 `[DONE]` 和 `finish_reason` 两种终止路径。

`FakeProvider`：

- 只实现 `ChatStream`。
- 把确定性响应逻辑抽成私有函数，`ChatStream` 根据最终消息发送 delta/done。
- 不引入 sleep 或真实异步计时。

### 测试迁移

删除或改写旧契约测试：

- 删除 `StreamingProvider` 编译契约测试。
- 删除“streaming provider 优先于 Chat fallback”的测试，因为 fallback 不再存在。

保留并强化：

- Agent 总是调用 `ChatStream`。
- text delta 顺序、tool call delta、tool execution、max steps、stream error 行为不变。
- OpenAI SSE usage-only chunk、tool call 聚合、error frame、malformed frame、`finish_reason`/`[DONE]` 终止行为不变。
- FakeProvider stream 返回确定性 tool call 和最终文本。

## 验收

- `go test ./internal/llms ./internal/agent` 通过。
- `go test ./...` 通过。
- `grep` 不再发现 `StreamingProvider` 类型定义或 Agent runtime type assertion。
- `internal/session` API 不改动。

## 风险

- 直接删除 `Chat` 会影响所有现有同步调用点；必须一次性迁移，不能留下 shim。
- `CollectChat` 若公开，必须明确它是 convenience helper，不是 Provider 第二契约。
- Agent 事件生命周期只剩 streaming 路径后，测试必须覆盖 stream 提前关闭、error event、max steps 终态拒绝。
