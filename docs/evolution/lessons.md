# OMP 架构学习归档

> 来源：`../oh-my-pi/` 与本项目后续开发实践。后续开发可先读本文获取方向；需要细节时直接阅读 OMP 源码。
> 维护规则：开发中发现可复用的架构判断、边界规则、踩坑经验或新的 OMP 借鉴点时，主动追加到本文；只记录会再次影响本项目的经验。

## 总判断

OMP 是一个 coding-agent harness：TUI 只是入口之一，核心是 `AgentSession + Agent Loop + Tools + Provider + Events` 组成的受控运行时。

本项目不应复刻 OMP 的完整产品复杂度，而应借鉴它的边界设计，先做一个 Go 版 mini agent harness。

## 当前项目应坚持的方向

- `internal/agent` 是 harness core：负责 loop、tool calling、事件、取消、步数上限。
- `internal/llms` 只做 Provider 抽象和协议适配，不让 Agent 认识 OpenAI 原始字段。
- `internal/tools` 只做工具声明、schema 暴露和执行分发。
- `cmd/*` 只做入口适配和展示，不承载 Agent 编排逻辑。
- 出现第二个入口（如 API/TUI）前，不新增 `internal/app` 或 `internal/harness` 空包。

## 从 OMP 学到的关键经验

### 1. Harness 不是 UI

OMP 的 TUI、print、RPC、ACP 都是同一个 Agent/session engine 的外层 adapter。

本项目也应让 CLI、未来 API、未来 TUI 共享同一个 `internal/agent`，不要为每个入口写一套 Agent 流程。

### 2. Agent core 必须与应用层分离

OMP 的 core loop 负责模型调用、tool call、tool result、事件和终止条件；session 层负责持久化、上下文、子任务、UI 通知。

本项目短期只需要 core loop。session、memory、resume 等长期状态等真实需求出现后再加。

### 3. 事件边界优先于 UI 绑定

OMP 通过事件把 Agent 内部状态暴露给 TUI/RPC/persistence。

本项目下一步应增加最小事件回调：

```text
agent_start
llm_request
tool_call
tool_result
final
agent_end
agent_error
```

先用简单 callback，不做 event bus。

### 4. Provider 只负责协议适配

OMP 将 provider 差异收敛在 AI/provider 层。

本项目应保持 `llms.Provider` 的统一输入输出，OpenAI-compatible、fake、未来其他 provider 都在 `internal/llms` 内转换。

### 5. Tool registry 与 active tools 要分开

OMP 有完整工具 registry，但每次只向模型暴露 active tools。

本项目短期可以只有 registry；当出现多业务 Agent 或危险工具时，再增加 active tool set。

### 6. 工具权限等危险工具出现后再做

OMP 有 read/write/exec approval、ACP 权限、bash 危险命令拦截等复杂机制。

本项目现在只有 calculator，不需要 approval。等出现 file write、shell exec、DB mutation、network mutation 后，再加：

```text
read: allow
write: prompt
exec: prompt
```

不要默认 yolo。

### 7. Bounded loop 是下一块核心能力

当前单轮 tool calling 只是 demo。真正 harness 需要：

```text
for step < MaxSteps:
    call LLM
    if no tool calls: return final
    execute tools
    append tool results
return max steps exceeded
```

默认 `MaxSteps = 4` 即可。

## 暂时不要复制的 OMP 能力

以下能力现在会过早复杂化：

- TUI
- MCP
- extension/plugin system
- subagent/task system
- memory/RAG/workflow
- streaming UI
- Responses API
- artifact spill
- native grep/pty/search
- provider catalog
- OAuth broker
- session persistence/resume

原则：等业务需要出现，再从 OMP 对应模块学习和裁剪。

## 推荐下一步开发顺序

1. CLI 支持真实用户输入，不再写死 prompt。
2. `agent.Options`：`Logger`、`MaxSteps`、`OnEvent`。
3. 扩展并接入 Agent events。
4. 将单轮 tool calling 改成 bounded loop。
5. 用 fake provider 覆盖：无工具、一轮工具、多轮工具、超过 MaxSteps、工具错误。
6. 只有出现第二入口时，再抽 `internal/app` composition root。
7. 只有出现危险工具时，再加 approval。

## 推荐阅读 OMP 路径

### 顶层结构

- `../oh-my-pi/README.md`
- `../oh-my-pi/AGENTS.md`
- `../oh-my-pi/packages/coding-agent/package.json`
- `../oh-my-pi/packages/agent/package.json`

### Agent core

- `../oh-my-pi/packages/agent/src/agent.ts`
- `../oh-my-pi/packages/agent/src/agent-loop.ts`
- `../oh-my-pi/packages/agent/src/types.ts`

### Session / app boundary

- `../oh-my-pi/packages/coding-agent/src/sdk.ts`
- `../oh-my-pi/packages/coding-agent/src/session/agent-session.ts`
- `../oh-my-pi/packages/coding-agent/src/main.ts`

### Tools / permissions

- `../oh-my-pi/packages/coding-agent/src/tools/index.ts`
- `../oh-my-pi/packages/coding-agent/src/tools/approval.ts`
- `../oh-my-pi/packages/coding-agent/src/tools/tool-result.ts`
- `../oh-my-pi/packages/agent/src/agent-loop.ts`

### Provider layer

- `../oh-my-pi/packages/ai/src/types.ts`
- `../oh-my-pi/packages/ai/src/utils/schema/wire.ts`
- `../oh-my-pi/packages/ai/src/utils/validation.ts`

## 本项目的判断规则

新增能力前先问：

1. 是否服务当前最小闭环？
2. 是否已有现成模块可以承载？
3. 是否会让 `internal/agent` 依赖 CLI/API/TUI？如果会，说明边界错了。
4. 是否只是为了“像 OMP”？如果是，先不做。
5. 是否能用 fake provider 写确定性测试？不能测试就不要急着抽象。

## 开发实践新增

- 当 Agent core 增加新能力时，优先用 `Options` 扩展配置面，避免继续增加 `NewWithXxx` 构造函数；旧构造函数只做兼容转发。
- Tool-calling loop 的步数应按“工具执行轮次”计数，而不是按 LLM 请求次数计数；这样 `MaxSteps=1` 表示允许一轮工具执行后再请求最终答案。
- CLI 入口只负责读取输入、组装依赖和打印答案；真实输入解析用小 helper 测试，不把交互逻辑塞进 Agent。
- 本地运行脚本负责加载 `.env`/`.env.local`、补齐本地配置和包装常用命令；不要把密钥或个人配置提交进仓库。
- OpenAI-compatible Provider 的职责是协议边界归一化：非 2xx 错误要保留有限响应体，响应里缺省的 `tool_call.type` 要补成内部契约使用的 `function`。
- Agent harness 对外应优先暴露结构化 `RunResult`，保留 `Run` 作为返回 answer 的兼容薄封装；后续 API/TUI 读取 trace，不反向侵入 Agent。
- `internal/harness` 负责组装 config、Provider、tools 和 Agent；CLI/API 只能调用 harness，不重复依赖装配，也不把入口层概念塞回 `internal/agent`。
- CLI 当前只是验证 Harness 的 smoke driver；新增 provider 选择、trace 等能力时，真实设计目标应落在 `internal/harness`，CLI 只暴露测试入口。
- 对话上下文先做无状态 history：调用方传入 `[]llms.Message`，Agent/Harness 只继续当前消息链；不要提前引入 session、memory、数据库或自动摘要。
- 学习 OMP 的 message 分层：Agent 内部用强语义 message，Provider 边界保留 `llms.Message` DTO；工具错误可进入 history，但由 `MaxSteps` 控制模型重试，不新增重复 retry 配置。

## 一句话原则

学习 OMP 的 harness 边界，不复制 OMP 的产品复杂度。
