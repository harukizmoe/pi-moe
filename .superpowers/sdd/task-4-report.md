# Task 4 报告：Agent 一轮 Tool Calling Loop

## RED 证据

- 测试文件：`internal/agent/loop_test.go`
- 测试提供者：`task-3.Task4Tester`
- 命令：`go test ./internal/agent -run TestAgentRunExecutesToolCall -v`
- 失败输出：

```text
# harukizmoe/pimoe/internal/agent [harukizmoe/pimoe/internal/agent.test]
internal/agent/loop_test.go:48:7: undefined: New
FAIL	 harukizmoe/pimoe/internal/agent [build failed]
FAIL
```

- 预期原因：测试先于实现落地，`internal/agent` 中尚未提供 `New`、`Agent`、`Run` 以及 tool calling 相关类型/循环逻辑，因此以未定义符号失败，符合 TDD 的 RED 阶段要求。

## GREEN 证据

- gofmt：`gofmt -w internal/agent/type.go internal/agent/agent.go internal/agent/loop.go internal/agent/tools.go internal/agent/events.go internal/agent/loop_test.go`
- 验收命令：`go test ./internal/agent -run TestAgentRunExecutesToolCall -v`
- 通过输出：

```text
=== RUN   TestAgentRunExecutesToolCall
--- PASS: TestAgentRunExecutesToolCall (0.00s)
PASS
ok  	harukizmoe/pimoe/internal/agent	0.003s
```

## 变更文件

- `internal/agent/type.go`
- `internal/agent/agent.go`
- `internal/agent/loop.go`
- `internal/agent/tools.go`
- `internal/agent/events.go`
- `internal/agent/loop_test.go`
- `.superpowers/sdd/task-4-report.md`

## 实现说明

- 新增 `RunRequest` / `RunResponse`，为 Agent 层保留清晰输入输出类型。
- 新增 `Agent` 构造函数 `New(provider, tools, model)`，只持有 `llms.Provider`、`tools.Registry` 和模型名，未引入配置读取、HTTP 路由或 provider 协议细节。
- 实现 `runToolCall`，把模型返回的 `llms.ToolCall` 直接转发给 `tools.Registry.Call`，并封装成标准 `llms.RoleTool` 消息。
- 实现 `Run(ctx, input)` 的一轮主循环：
  1. 以 user message 发起第一次 chat，并附带 `a.tools.Schemas()`；
  2. 若 assistant 未请求工具，直接返回文本回答；
  3. 若 assistant 返回多个 tool calls，则逐个执行并把 tool result message 追加进对话历史；
  4. 进行第二次 chat，返回最终回答。
- 新增轻量 `EventType` / `Event` 定义，保留 `tool_call` 和 `final` 两类事件常量，满足当前任务范围。
- 测试通过 `recordingProvider` 记录两次 `ChatRequest`，断言：
  - 第一次 chat 带上 calculator tool schema；
  - 第二次 chat 包含 assistant tool call 与对应 tool result message；
  - 最终回答为 `13 * 7 = 91`。

## 注释合规说明

- 所有导出的 Go 标识符均补充中文注释：`RunRequest`、`RunResponse`、`Agent`、`New`、`Run`、`EventType`、`EventToolCall`、`EventFinal`、`Event`。
- 导出结构体字段补充中文注释：`RunRequest.Input`、`RunResponse.Answer`、`Event.Type`、`Event.Message`。
- 在 `Run` 内仅对关键步骤添加中文说明，重点解释为什么第一次要携带 tools schema，以及为什么需要先完整执行同一轮中的全部 tool calls 再进行最终 chat，避免逐行噪声注释。

## Self-review

- 变更严格限制在 Task 4 指定文件，没有修改 `internal/config`、`internal/llms`、`internal/tools`、CLI 或 HTTP 相关代码。
- `internal/agent` 仅依赖 `llms.Provider` 与 `tools.Registry`，符合模块边界要求，没有泄漏 provider-specific HTTP 细节。
- 当前实现故意只支持“一轮 tool calling + 最终回答”的最小闭环；若后续要支持多轮递归、流式输出或事件广播，应在此基础上扩展，但本任务未提前引入额外抽象。
- 测试是 focused 闭环验证，只运行 Task 4 验收命令，符合“只跑最小可证明测试”的约束。
