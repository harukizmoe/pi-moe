# Agent Message 设计

## 背景

当前 `internal/llms.Message` 是一个 Provider-neutral 宽结构体：

```go
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}
```

它便于映射 OpenAI-compatible chat-completions 协议，但也允许非法状态，例如 user message 带 `ToolCalls`、tool result 缺少 `ToolCallID`、assistant message 带 `ToolCallID`。随着 Agent 支持无状态 history、tool calling trace 和后续上下文压缩，Agent 内部语义不应继续直接依赖这个宽 DTO。

OMP 的经验是分层：Agent 内部使用 `AgentMessage`，调模型前通过 `convertToLlm` 转成 LLM 能理解的 `Message`。本项目采用同样的边界，但只实现当前 CLI + fake provider 闭环需要的最小版本。

## 目标

- 在 `internal/agent` 引入强语义 message 类型，防止 Agent 内部构造非法对话状态。
- 保留 `internal/llms.Message` 作为 Provider-neutral DTO，不把 Agent 运行状态塞进 Provider 边界。
- 支持工具执行错误进入 history，让模型基于错误信息恢复或重新发起 tool call。
- 继续用 `MaxSteps` 限制 tool-calling 循环，不新增重复的 retry 配置。
- 保持现有 `Run`、`RunResult`、`RunMessages([]llms.Message)` 兼容。

## 非目标

- 不引入 session、memory、数据库或自动历史持久化。
- 不引入多模态 content block、thinking block、custom UI message。
- 不把 `llms.Message` 改成 interface 或 union 风格类型。
- 不实现 Agent 自动重放同一个失败 tool call。
- 不新增 `MaxToolRetries`、`MaxToolErrors` 等第二套循环控制。

## 架构

```text
internal/agent.Message
  Agent 内部强语义消息，表达 user、assistant、tool result。

internal/llms.Message
  Provider-neutral LLM 上下文 DTO，贴近 chat-completions 形状。

internal/llms/openai_compatible.go
  OpenAI-compatible wire DTO 映射，继续只处理协议转换。
```

调用路径：

```text
[]agent.Message
  -> toLLMMessages
  -> []llms.Message
  -> llms.Provider.Chat
  -> llms.Message assistant response
  -> agent.AssistantMessage / agent.ToolResultMessage
```

`agent.Message` 表达业务语义；`llms.Message` 表达发送给 Provider 的标准化上下文；OpenAI adapter 继续负责 wire format。

## 类型设计

初版新增在 `internal/agent`：

```go
type Message interface {
	agentMessage()
}

type UserMessage struct {
	Content string
}

type AssistantMessage struct {
	Content   string
	ToolCalls []llms.ToolCall
}

type ToolResultMessage struct {
	ToolCallID string
	ToolName   string
	Content    string
	IsError    bool
}
```

约束：

- `UserMessage.Content` trim 后不能为空。
- `AssistantMessage` 必须至少有 `Content` 或 `ToolCalls`。
- `ToolResultMessage.ToolCallID` 必须非空。
- `ToolResultMessage.ToolName` 必须非空，用于 trace、日志和错误摘要。
- `ToolResultMessage.IsError=false` 表示成功工具结果。
- `ToolResultMessage.IsError=true` 表示工具失败结果；`Content` 必须是可安全暴露给模型的错误摘要。

## 转换规则

新增转换函数：

```go
func toLLMMessages(messages []Message) ([]llms.Message, error)
```

规则：

```text
UserMessage
  -> llms.Message{Role: llms.RoleUser, Content: trimmedContent}

AssistantMessage
  -> llms.Message{Role: llms.RoleAssistant, Content: content, ToolCalls: copiedToolCalls}

ToolResultMessage
  -> llms.Message{Role: llms.RoleTool, ToolCallID: toolCallID, Content: content}
```

转换必须拷贝 `ToolCalls` slice，避免调用方在运行中修改 history。

## API 兼容

新增强语义入口：

```go
func (a *Agent) RunAgentMessages(ctx context.Context, messages []Message) (*RunResult, error)
func (h *Harness) RunAgentMessages(ctx context.Context, messages []agent.Message) (*agent.RunResult, error)
```

保留现有入口：

```go
func (a *Agent) RunMessages(ctx context.Context, messages []llms.Message) (*RunResult, error)
func (h *Harness) RunMessages(ctx context.Context, messages []llms.Message) (*agent.RunResult, error)
```

兼容入口继续接受 `llms.Message`，但必须先校验并转换成 `agent.Message`，再复用 `RunAgentMessages` 主路径。外部调用方无需一次性迁移。新代码优先使用 `RunAgentMessages`。

## 工具错误策略

工具错误进入 history，但不做 Agent 自动重放。

流程：

```text
assistant tool call
  -> 本地工具执行
  -> 成功：追加 ToolResultMessage{IsError:false}
  -> 失败：追加 ToolResultMessage{IsError:true, Content:安全错误摘要}
  -> 下一轮 LLM 根据 tool result 决定 final answer 或新的 tool call
```

工具失败时：

- `RunResult.Step.Error` 保存原始本地错误。
- `ToolResultMessage.Content` 保存可安全暴露给模型的错误摘要。
- Agent 不直接返回工具错误。
- 如果模型收到错误后输出 final answer，则 `RunResult.Answer` 有值且 `err == nil`。
- 如果模型持续调用工具，仍由 `MaxSteps` 截断。

错误摘要的初版格式固定为：

```text
tool "<toolName>" failed: <sanitized error>
```

安全要求：

- 不向模型发送 API Key、完整配置、密钥环境变量名、panic 堆栈或不必要的本地隐私内容。
- 当前本地 calculator 错误可以直接摘要；后续引入文件、网络、数据库工具时必须重新审查错误清洗策略。

## 重试与循环上限

第一版不新增 `MaxToolRetries`。

规则：

- Agent 不自动重复执行同一个失败 tool call。
- 模型可以根据 `IsError=true` 的 tool result 发起新的 tool call。
- 每一轮 assistant tool call 都消耗一次 `MaxSteps`，无论工具结果成功或失败。
- `MaxSteps` 是当前唯一的 tool-calling 循环保护。

原因：

- 当前失败多为参数错误或工具校验失败，重复执行同一参数没有价值。
- `MaxSteps` 已经能防止模型无限重试。
- 额外的 `MaxToolRetries` 会和 `MaxSteps` 产生重叠语义。
- 真正的自动重试只适合 transient 外部错误，应等引入网络/数据库等工具后按错误类型设计。

## RunResult 语义

保留现有结构：

```go
type Step struct {
	ToolCallID string
	ToolName   string
	Arguments  string
	Result     string
	Error      string
}
```

成功工具调用：

```text
Result 非空
Error 为空
```

工具失败但模型恢复：

```text
Result 为空
Error 非空
RunResult.Answer 是模型最终回答
RunResult 返回 err=nil
```

这些情况仍返回 `err`：

- message/history 校验失败。
- Provider `Chat` 失败。
- context 取消。
- 超过 `MaxSteps`。
- 模型返回非法 assistant message。

## 事件与日志

第一版可以继续复用 `EventToolResult` 表示 tool result 进入 history。事件 `Message` 使用发送给模型的安全摘要。

日志要求：

- `agent.tool.error` 记录本地错误，但遵守日志脱敏规则。
- `agent.tool.result` 记录安全 content。
- 不记录 API Key、完整密钥配置或不必要隐私内容。

后续如果 UI 需要区分成功/失败，可新增 `EventToolError`，但第一版不强制。

## 测试要求

必须按 TDD 增加测试：

1. `RunAgentMessages` 能把 `[]agent.Message` 转为 Provider 请求，并保持现有 history 顺序。
2. `RunAgentMessages` 拒绝空 history。
3. `RunAgentMessages` 拒绝最后一条不是非空 user message。
4. 转换拒绝非法 message：空 user、空 assistant、tool result 缺 `ToolCallID`、tool result 缺 `ToolName`。
5. tool 成功路径保持当前 `Run` / `RunResult` 行为。
6. tool 失败时不直接返回 err，而是追加 `IsError=true` 的 tool result 给下一轮 LLM。
7. tool 失败后模型输出 final answer 时，`RunResult.Answer` 有值、`err == nil`、`Step.Error` 非空。
8. tool 失败后模型继续重试时，每轮仍受 `MaxSteps` 限制。
9. 现有 `RunMessages([]llms.Message)` 兼容入口仍通过原测试。

最小验证命令：

```bash
go test ./internal/agent ./internal/harness
go test ./...
go vet ./...
```

## 迁移顺序

1. 新增 `internal/agent` message 类型和转换测试。
2. 新增 `RunAgentMessages`，让 `RunResult`/`Run` 复用强语义入口。
3. 修改 tool error 路径：失败时生成 `ToolResultMessage{IsError:true}` 并继续下一轮。
4. 保留并验证 `RunMessages([]llms.Message)` 兼容入口。
5. 更新 harness 暴露 `RunAgentMessages`。
6. 运行最小验证命令。

## 风险

- 工具错误进入模型后，模型可能根据错误继续调用工具，必须依赖 `MaxSteps` 截断。
- 错误摘要如果不清洗，可能泄漏内部信息。当前工具简单，后续扩展工具时必须复查。
- `RunResult` 语义变化：工具失败不一定返回 `err`，调用方需要看 `Steps[].Error` 判断工具层失败。
- `llms.Message` 兼容入口仍可能接收非法宽结构；实现时必须在兼容层做校验或转换。
