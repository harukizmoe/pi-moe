# Event-first Harness Session 设计

## 背景

当前 `internal/agent` 同时暴露两条运行路径:

- `RunAgentMessages(ctx, []Message) (*RunResult, error)`
- `StreamAgentMessages(ctx, []Message) <-chan Event`

`RunResult` 保存 `Answer`、`ToolRounds`、`Messages`、`Steps`。这些字段都能由事件流重建,因此它成为第二套事实来源。`internal/harness` 又重复暴露 `Run/RunAgentMessages` 和 `Stream`,导致入口层需要理解同步结果与流式事件的差异。

Tau/OMP 的参考方向更清晰:

- agent loop 以事件为主契约。
- 完整 message 只在 terminal event 中落地。
- 同步结果只是事件流的外部消费方式,不进入 core API。
- harness/session 负责 transcript、取消、监听器和入口状态。

## 目标

1. `internal/agent` 保持无状态:接收调用方传入的 transcript,执行一轮 loop,实时输出事件。
2. `internal/harness` 改为 Tau 式 stateful session owner:持有 transcript、运行状态、取消入口和事件订阅。
3. 移除 `RunResult` 作为 agent/harness 主契约;answer、trace、transcript 都从事件派生。
4. 事件模型扩展到 message lifecycle,支持未来 provider streaming:
   - message start
   - content/thinking/tool-call delta
   - message end
5. `MessageEndEvent` 携带完整 assistant message,作为 transcript 持久化边界。
6. `ToolExecutionEndEvent` 携带完整 tool result message,作为 tool result 持久化边界。

## 非目标

- 不在本阶段实现 HTTP API、数据库、memory、streaming provider 或 Responses API。
- 不引入 session persistence、branch、compaction、system prompt versioning。
- 不把 provider 原生事件完整照搬到 public API;只保留本项目当前需要的稳定语义。
- 不保留 `RunResult` 作为对外主路径。若 CLI 需要同步 answer,只能在 CLI 或测试 helper 中 collect events。

## 架构

```text
internal/agent
  Agent.Stream(ctx, messages) <-chan Event
  - 无状态
  - 不保存 transcript
  - 不返回 RunResult
  - 不知道 harness/session

internal/harness
  Session
  - 保存 transcript
  - 暴露 Prompt/Events/Messages/Cancel
  - 消费 agent events
  - 在 message/tool-result terminal event 更新 transcript

cmd/cli
  - 创建 harness session
  - 发送 prompt
  - 消费 events
  - 如需最终 answer/trace,本地 collect events
```

`agent` 是纯 loop。`harness` 是运行时会话。CLI/API/TUI 是事件消费者。

## Agent API

推荐 API:

```go
func (a *Agent) Stream(ctx context.Context, messages []Message) <-chan Event
```

删除或退役:

```go
func (a *Agent) RunAgentMessages(ctx context.Context, messages []Message) (*RunResult, error)
type RunResult struct { ... }
```

如果迁移需要短期兼容,只允许把旧同步入口移动到测试 helper 或 CLI 内部 collector;不继续作为 `internal/agent` 的设计中心。

## Harness API

推荐最小 API:

```go
type Session struct { ... }

func (h *Harness) NewSession() *Session
func (s *Session) Prompt(ctx context.Context, input string) <-chan Event
func (s *Session) Events() <-chan Event
func (s *Session) Messages() []agent.Message
func (s *Session) Cancel()
```

语义:

- `Prompt` 追加 user message,启动一轮 agent stream,并返回本轮事件流。
- `Session` 内部同步消费 agent events,维护 transcript。
- `Messages` 返回 transcript snapshot,必须复制 slice 和可变字段。
- `Cancel` 取消当前运行;没有运行时是 no-op。
- 同一 session 同时只允许一个 active turn。若 active turn 未结束再次 `Prompt`,返回只包含 `ErrorEvent` 的 closed stream,不修改 transcript。

## Event 模型

保留强类型事件,但重命名为更接近 lifecycle 的语义。

```go
type Event interface { AgentEvent() }

type RunStartEvent struct {
	RunID string
}

type TurnStartEvent struct {
	RunID string
	Turn int
	UserMessage UserMessage
}

type MessageStartEvent struct {
	RunID string
	MessageID string
	Role string
}

type MessageDeltaEvent struct {
	RunID string
	MessageID string
	Kind MessageDeltaKind
	ContentIndex int
	Delta string
}

type MessageEndEvent struct {
	RunID string
	MessageID string
	Message AssistantMessage
}

type ToolExecutionStartEvent struct {
	RunID string
	ToolCallID string
	ToolName string
	Arguments string
}

type ToolExecutionEndEvent struct {
	RunID string
	ToolCallID string
	Result ToolResultMessage
	Error error
}

type TurnEndEvent struct {
	RunID string
	Turn int
}

type RunEndEvent struct {
	RunID string
}

type ErrorEvent struct {
	RunID string
	Error error
}
```

`MessageDeltaKind` 初始支持:

```go
type MessageDeltaKind string

const (
	MessageDeltaText MessageDeltaKind = "text"
	MessageDeltaThinking MessageDeltaKind = "thinking"
	MessageDeltaToolCall MessageDeltaKind = "tool_call"
)
```

当前 provider 仍是非 streaming 时,agent 也必须发出完整 lifecycle:

```text
MessageStart
MessageDelta(text, full content)        // content 非空时
MessageEnd(full AssistantMessage)
```

出现 tool call 时:

```text
MessageStart
MessageDelta(tool_call, arguments)      // 可选;当前可一次性输出完整 arguments
MessageEnd(AssistantMessage{ToolCalls: ...})
ToolExecutionStart
ToolExecutionEnd(ToolResultMessage)
```

未来接入 streaming provider 时,只需要把多块 token 映射到多次 `MessageDeltaEvent`;`MessageEndEvent` 仍携带完整 assembled message。

## Transcript 持久化规则

Harness 只在 terminal event 更新 transcript:

1. `Prompt` 接收非空 input 后立即 append `UserMessage`。
2. 收到 `MessageEndEvent` 后 append 完整 `AssistantMessage`。
3. 收到 `ToolExecutionEndEvent` 后 append 完整 `ToolResultMessage`。
4. `MessageDeltaEvent` 只用于 UI/日志,不持久化。
5. `ErrorEvent` 不自动合成 assistant message;已收到的 terminal message 保留,未结束的 partial message 丢弃。

这与 OMP 的 `done/error` terminal payload 对齐:delta 是展示层事实,terminal message 是 transcript 事实。

## Tool calling 数据流

```text
Session.Prompt(input)
  append UserMessage
  Agent.Stream(snapshot)
    provider chat
    MessageStart/Delta/End assistant
    if assistant has tool calls:
      ToolExecutionStart
      execute tool
      ToolExecutionEnd tool result
      next provider chat with updated transcript
    else:
      TurnEnd
      RunEnd
```

Agent 内部每轮 provider call 使用本轮已追加的 local transcript。Harness 也通过 terminal events 得到同样的 transcript,但 agent 不依赖 harness 状态。

## Error 语义

- 输入为空:Session 返回 `ErrorEvent`,不追加 user message。
- provider error:Agent 发出 `ErrorEvent`,turn 结束;harness 不追加 partial assistant。
- tool error:不是 fatal error。Agent 发出 `ToolExecutionEndEvent{Result.IsError:true, Error: err}`,并继续下一轮 provider call。
- max steps:Agent 发出 `ErrorEvent`;已完成的 assistant/tool result 保留。
- context cancelled:Agent 尽力发出 `ErrorEvent{Error: ctx.Err()}` 后关闭事件流;如果取消发生在发送事件期间,允许直接关闭。Harness 必须清理 active turn。

## CLI 同步输出

CLI 不再调用 `RunResult`。需要 answer 或 trace 时,CLI 在本地 collect events:

- answer:最后一个 `MessageEndEvent` 中无 tool call 的 assistant text。
- trace:所有 `ToolExecutionStartEvent` / `ToolExecutionEndEvent`。
- transcript:来自 session `Messages()` snapshot。

collector 是入口层 adapter,不是 agent/harness core 类型。

## 测试策略

1. Agent event lifecycle:
   - no-tool answer 输出 `MessageStart -> MessageDelta -> MessageEnd -> TurnEnd -> RunEnd`。
   - tool call 输出 assistant `MessageEnd`,然后 tool start/end,再下一轮 final assistant。
2. Harness transcript:
   - `Prompt` 后 user message 被保存。
   - `MessageEndEvent` 后 assistant message 被保存。
   - `ToolExecutionEndEvent` 后 tool result 被保存。
   - delta 不直接修改 transcript。
3. Error:
   - provider error 不保存 partial assistant。
   - tool error 保存 `ToolResultMessage{IsError:true}` 并继续。
   - max steps 返回 error event 且保留已完成 terminal messages。
4. CLI collector:
   - 从 events 派生 answer 和 trace。
   - 不依赖 `RunResult`。

## 迁移步骤

1. 新增 message lifecycle events,保留旧事件测试作为迁移参考。
2. 改 agent loop 为 event-only,删除 `collectRunResult` 在 core 中的使用。
3. 引入 `harness.Session`,把 transcript 维护移到 harness。
4. 改 CLI 为 session + event collector。
5. 删除 `RunResult` 主 API 和相关同步 harness API。
6. 清理旧事件别名和旧测试。

## 风险与边界

- 事件命名一旦外露会成为 API,初版只定义当前需要的字段。
- `Messages()` 必须防御性复制,避免调用方修改 session 内部 transcript。
- 同一 session 并发 prompt 会破坏 transcript 顺序,初版直接拒绝。
- 当前 provider 非 streaming,但事件模型必须先兼容 streaming,避免未来再次改 API。
