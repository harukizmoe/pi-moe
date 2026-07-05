# 日志系统设计

**目标：** 为当前 Go Agent 项目提供一个统一、轻量、开发期友好的日志系统，让 CLI/API/Agent 能观察关键运行过程，尤其是 Agent tool calling 的执行链路。

**已确认方向：** 日志系统放在 `internal/logger`。它服务当前项目，不作为外部 Go 项目的公共库；`cmd/cli`、`cmd/api` 和 `internal/*` 都可以 import。

## 设计原则

- 使用 Go 标准库 `log/slog`，不引入 zap、logrus、zerolog 等新依赖。
- 入口程序负责创建 logger，并通过构造函数传给需要日志的模块。
- 不使用全局 logger，不依赖 `slog.Default()`。
- logger 包只提供日志抽象和基础实现，不包含 Agent、HTTP、LLM 的业务知识。
- 日志注释和新增 Go 注释统一使用中文。
- 开发期优先可读性；生产级日志文件、轮转、trace、OpenTelemetry 暂不实现。

## 模块职责

### `internal/logger`

统一日志基础设施。

职责：

- 定义项目内部统一的 `Logger` 接口。
- 提供开发期 logger：输出到 stdout，默认 debug 级别。
- 提供 noop logger：测试默认使用，避免污染测试输出。
- 适配 `slog` 的结构化字段。

不负责：

- 读取配置文件。
- 定义 Agent 事件。
- 拼装业务字段。
- HTTP middleware。
- LLM payload 脱敏。

### `internal/agent`

负责记录 Agent 运行过程。

职责：

- 在关键阶段调用注入的 logger。
- 记录输入、模型、工具数量、tool call、tool result、最终答案和错误阶段。
- 不关心 logger 的具体实现。

### `cmd/cli`

负责开发期日志初始化。

职责：

- 创建 `logger.NewDevelopment()`。
- 通过 `agent.NewWithLogger(...)` 注入 Agent。
- 保持 CLI 输出最终答案。

## API 设计

### Logger 接口

```go
type Logger interface {
	Debug(ctx context.Context, msg string, attrs ...any)
	Info(ctx context.Context, msg string, attrs ...any)
	Error(ctx context.Context, msg string, attrs ...any)
}
```

说明：

- `ctx` 预留给未来 request id、trace id 等上下文字段。
- `attrs ...any` 直接采用 slog 风格键值对，避免再设计一套字段结构。
- 当前不做 `Warn`，因为现阶段 Agent 流程只有 debug/info/error 三类足够。

### 构造函数

```go
func NewDevelopment() Logger
func NewNoop() Logger
```

`NewDevelopment()`：

- 输出到 stdout。
- level = debug。
- 使用 `slog.TextHandler`，方便本地终端阅读。

`NewNoop()`：

- 丢弃所有日志。
- 用于测试和默认构造。

## Agent 接入设计

保留现有构造函数，避免破坏测试和调用方：

```go
func New(provider llms.Provider, tools *tools.Registry, model string) *Agent
```

新增显式注入日志的构造函数：

```go
func NewWithLogger(provider llms.Provider, tools *tools.Registry, model string, logger logger.Logger) *Agent
```

内部结构：

```go
type Agent struct {
	provider llms.Provider
	tools    *tools.Registry
	model    string
	logger   logger.Logger
}
```

`New(...)` 默认使用 `logger.NewNoop()`，保证测试不产生日志噪声。

## Agent 日志点

### 启动

```text
agent.run.start
```

字段：

- `model`
- `input`

### 第一次 LLM 请求前

```text
agent.llm.first.request
```

字段：

- `messages`
- `tools`

### 第一次 LLM 请求失败

```text
agent.llm.first.error
```

字段：

- `error`

### 第一次 LLM 直接返回最终内容

```text
agent.llm.first.final
```

字段：

- `content`

### 收到 tool calls

```text
agent.tool_calls.received
```

字段：

- `count`

### 执行工具前

```text
agent.tool.call
```

字段：

- `name`
- `arguments`

### 工具执行失败

```text
agent.tool.error
```

字段：

- `name`
- `error`

### 工具执行成功

```text
agent.tool.result
```

字段：

- `name`
- `content`

### 第二次 LLM 请求前

```text
agent.llm.final.request
```

字段：

- `messages`

### 第二次 LLM 请求失败

```text
agent.llm.final.error
```

字段：

- `error`

### 第二轮 tool call 被拒绝

```text
agent.llm.final.unsupported_tool_calls
```

字段：

- `count`

### 完成

```text
agent.run.done
```

字段：

- `answer`

## 示例输出

```text
time=2026-07-05T22:30:01 level=INFO msg=agent.run.start model=gpt-5.4-mini input="use calculator to compute 13 * 7"
time=2026-07-05T22:30:01 level=DEBUG msg=agent.llm.first.request messages=1 tools=1
time=2026-07-05T22:30:02 level=INFO msg=agent.tool_calls.received count=1
time=2026-07-05T22:30:02 level=DEBUG msg=agent.tool.call name=calculator arguments="{\"a\":13,\"b\":7,\"op\":\"mul\"}"
time=2026-07-05T22:30:02 level=DEBUG msg=agent.tool.result name=calculator content=91
time=2026-07-05T22:30:02 level=DEBUG msg=agent.llm.final.request messages=3
time=2026-07-05T22:30:03 level=INFO msg=agent.run.done answer=91
91
```

## 暂不实现

- 日志文件输出。
- 日志轮转。
- YAML 配置日志级别。
- request id / trace id。
- OpenTelemetry。
- HTTP middleware logger。
- 统一脱敏系统。
- LLM 原始请求/响应完整 dump。

这些等 API、并发请求或生产部署出现后再加。

## 验收标准

- `internal/logger` 提供 `Logger`、`NewDevelopment()`、`NewNoop()`。
- `agent.New(...)` 保持兼容，默认不输出日志。
- `agent.NewWithLogger(...)` 可注入开发日志。
- `cmd/cli` 使用 `logger.NewDevelopment()`，运行时能看到 Agent 关键步骤。
- 现有 agent 测试不因为日志产生额外输出。
- `go run ./cmd/cli` 仍输出最终答案。
- `go test ./...` 通过。
