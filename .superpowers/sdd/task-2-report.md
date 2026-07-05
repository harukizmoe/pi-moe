# Task 2 报告：LLM 统一类型、Registry 和 Fake Provider

## RED 证据

执行命令：

```bash
go test ./internal/llms -run TestFakeProviderReturnsToolCallThenFinalAnswer -v
```

失败输出：

```text
# harukizmoe/pimoe/internal/llms [harukizmoe/pimoe/internal/llms.test]
internal/llms/fake_test.go:9:19: undefined: NewFakeProvider
internal/llms/fake_test.go:14:52: undefined: ChatRequest
internal/llms/fake_test.go:15:15: undefined: Message
internal/llms/fake_test.go:27:53: undefined: ChatRequest
internal/llms/fake_test.go:28:15: undefined: Message
FAIL\tharukizmoe/pimoe/internal/llms [build failed]
FAIL
```

结论：测试先于实现创建，且按预期因统一类型与 fake provider 缺失而失败，满足 TDD 的 RED。

## 实现说明

### 1. `internal/llms/type.go`
- 新增统一协议类型：`Role`、`ChatRequest`、`ChatResponse`、`Message`、`Tool`、`ToolFunction`、`ToolCall`、`ToolCallFunction`。
- 统一 `assistant/tool/user/system` 消息表达，供后续 Agent loop 与 provider 复用。

### 2. `internal/llms/provider.go`
- 定义 `Provider` 接口：`Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)`。
- 定义 `Factory`。
- 定义 `Registry`，支持 `Register` 和 `NewProvider`。
- 未新增平行抽象，保留最小 provider 注册能力。

### 3. `internal/llms/fake.go`
- 实现 `FakeProvider` 与 `NewFakeProvider`。
- 第一轮 `Chat`：确定性返回 `calculator` function tool call，`ToolCallID` 固定为 `call_fake_calculator`。
- 第二轮 `Chat`：检测最后一个 `tool` 消息，返回最终内容 `13 * 7 = 91`。
- 无网络访问，无随机性。

### 4. `internal/llms/fake_test.go`
- 新增聚焦测试 `TestFakeProviderReturnsToolCallThenFinalAnswer`。
- 覆盖两阶段行为：
  1. 首次调用返回 tool call。
  2. 收到 tool message 后返回最终回答。

## GREEN 证据

执行命令：

```bash
go test ./internal/llms -run TestFakeProviderReturnsToolCallThenFinalAnswer -v
```

通过输出：

```text
=== RUN   TestFakeProviderReturnsToolCallThenFinalAnswer
--- PASS: TestFakeProviderReturnsToolCallThenFinalAnswer (0.00s)
PASS
ok  \tharukizmoe/pimoe/internal/llms\t0.003s
```

结论：Fake provider 的确定性 tool call → tool result → final answer 闭环已通过验收测试。

## 文件变更

- 新增：`internal/llms/type.go`
- 修改：`internal/llms/provider.go`
- 修改：`internal/llms/fake.go`
- 新增：`internal/llms/fake_test.go`

## 自检

- [x] 遵守 `AGENT.md`：实现保留在 `internal/llms`，未新增 `internal/ai`。
- [x] 按 TDD 执行：先写测试、确认 RED、再实现、确认 GREEN。
- [x] Fake provider 无网络访问，返回确定性 tool call 与最终回答。
- [x] 未实现 Task 5 的 OpenAI-compatible provider。
- [x] 仅运行聚焦测试：`go test ./internal/llms -run TestFakeProviderReturnsToolCallThenFinalAnswer -v`。
