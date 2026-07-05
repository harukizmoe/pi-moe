# LLM Tool Calling 练习设计

**目标：** 用 Go 构建一个小型练习项目：`internal/llms` 承担 `oh-my-pi/packages/ai` 的 AI 层职责，统一 LLM 协议类型，提供 OpenAI-compatible Provider，使用 Viper 从 YAML 加载配置，并通过最小 Agent Loop 跑通 tools call。

**已确认方向：** `internal/llms` 就是本项目的 AI 层；不新增 `internal/ai`。

## 当前结构

```text
cmd/
  api/
  cli/

configs/
  providers.yaml

internal/
  llms/
    config.go
    provider.go
    openai_compatible.go
    fake.go

  agent/
    agent.go
    loop.go
    type.go
    tools.go
    events.go

  application/
    router/
    middleware/
    service/
    data/

  tools/
  config/
  logger/
```

## 模块职责

### `internal/llms`

对应 `oh-my-pi/packages/ai`。

```text
packages/ai/src/types.ts          -> internal/llms/type.go
packages/ai/src/api-registry.ts   -> internal/llms/provider.go
packages/ai/src/providers/*.ts    -> internal/llms/openai_compatible.go
packages/ai/src/providers/mock.ts -> internal/llms/fake.go
```

职责：

- 定义统一 Message、Tool、ToolCall、ChatRequest、ChatResponse 类型。
- 定义 Provider 接口。
- 按 Provider 类型注册工厂。
- 基于 YAML 配置创建 Provider。
- 将内部 ChatRequest 适配到 OpenAI-compatible `/v1/chat/completions`。
- 隔离 Provider HTTP 细节，不泄漏到 `agent` 和 `application`。

`llms` 不执行工具，只处理模型协议。

### `internal/agent`

负责 tool calling 主循环。

职责：

- 接收用户输入。
- 构造消息历史。
- 将消息和工具 schema 发送给 `llms.Provider`。
- 通过 `tools.Registry` 执行模型返回的 tool calls。
- 追加 `assistant` 和 `tool` 消息。
- 再次请求模型生成最终回答。

当前阶段只支持一轮 tool call；如果后续需要多轮，必须加 `max_steps` 上限。

### `internal/tools`

负责本地工具。

职责：

- 定义 Tool 接口。
- 按名称注册工具。
- 将工具定义转换为 `[]llms.Tool` schema。
- 按 tool name 分发执行。

当前阶段只实现 `calculator`。

### `internal/config`

负责 Viper 和配置读取。

职责：

- 读取 `configs/providers.yaml`。
- 解析环境变量。
- 返回强类型配置。
- 不让 `llms`、`agent`、`tools` 直接依赖 Viper。

### `internal/application`

保留给后续 HTTP/business 入口。

CLI 练习阶段不依赖 router、middleware、data。CLI 路径跑通后，API 路径再按下面方式接入：

```text
cmd/api/main.go
  -> application/router
  -> application/middleware
  -> application/service
  -> agent
  -> llms + tools
```

## 配置形态

Provider 实例名和实现类型分开：

```yaml
llms:
  default_provider: deepseek

  providers:
    openai:
      type: openai_compatible
      base_url: "https://api.openai.com/v1"
      api_key_env: "OPENAI_API_KEY"
      model: "gpt-4o-mini"
      timeout_seconds: 60

    deepseek:
      type: openai_compatible
      base_url: "https://api.deepseek.com/v1"
      api_key_env: "DEEPSEEK_API_KEY"
      model: "deepseek-chat"
      timeout_seconds: 60

    fake:
      type: fake
      model: "fake-tool-model"
      timeout_seconds: 1
```

理由：

- `deepseek` 和 `openai` 是 Provider 实例名。
- `openai_compatible` 是 Provider 实现类型。
- `api_key_env` 避免 `${ENV}` 字符串展开逻辑。

## 运行流

```text
cmd/cli/main.go
  -> config.Load("configs/providers.yaml")
  -> llms.NewRegistry()
  -> 注册 openai_compatible 和 fake 工厂
  -> 根据 llms.default_provider 创建 Provider
  -> tools.NewRegistry()
  -> 注册 calculator
  -> agent.New(provider, tools, model)
  -> agent.Run("use calculator to compute 13 * 7")
```

Agent 流程：

```text
user message
  -> llms.Chat(messages + tool schemas)
  -> 模型返回 content 或 tool_calls
  -> 如果是 content：直接返回
  -> 如果是 tool_calls：
       追加 assistant message
       执行每个 tool
       追加 tool result messages
       再次调用 llms.Chat
       返回最终 content
```

## 当前阶段不做

- HTTP router 和 middleware。
- 数据库/data 层。
- 长期记忆。
- Multi-agent 编排。
- Streaming responses。
- OpenAI Responses API。
- Usage 统计。
- OAuth/auth broker。
- Rate-limit 轮换。

## 验收标准

- `go run ./cmd/cli` 能通过 Viper 读取 `configs/providers.yaml`。
- `fake` Provider 能无网络、确定性地触发一次 tool call。
- `openai_compatible` Provider 能发送 OpenAI Chat Completions-compatible JSON。
- Agent 能执行 `calculator` 工具，并把 tool result 发回模型。
- LLM 协议类型只放在 `internal/llms`。
- 工具执行只放在 `internal/tools` 和 `internal/agent`。
