# 审计 OMP 与 Tau 的可迁移 Agent 机制

Type: research
Status: resolved

## Question

审计 `../oh-my-pi` 与 `../tau` 的 Agent loop、context 管理、记忆和工具执行机制。哪些模式可迁移到业务无关的后端 Agent Runtime，哪些依赖 coding-agent 的终端、文件系统、工作区或 TUI，因而必须排除？结论须链接到具体源码或文档。

## Answer

完整证据分别位于：

- [OMP Agent Runtime 机制审计](../research/omp-patterns.md)
- [Tau 可迁移 Agent Runtime 机制审计](../research/tau-patterns.md)

### 可迁移的共同机制

1. **无持久状态的 loop 核心**：一次运行由 provider、instructions、conversation、tools 和取消信号驱动；assistant 与 tool result 追加过程通过 typed events 对外可见。OMP 的 loop 接收 context 副本并返回本次新增消息，Tau 的纯 loop 同样把 durable transcript 留给调用方（OMP `packages/agent/src/agent-loop.ts:302-381`；Tau `src/tau_agent/loop.py:37-166`）。
2. **Agent context 与 provider wire message 分离**：Runtime 保留强语义消息，在发请求前再执行 context transform、过滤和 provider 投影；不能让 provider schema 成为 Runtime 的领域模型（OMP `packages/agent/src/types.ts:118-167`、`packages/agent/src/agent-loop.ts:1173-1212`；Tau `src/tau_ai/provider.py:21-34`、`src/tau_agent/messages.py:13-47`）。
3. **工具调用/result 配对不变量**：未知工具、执行异常、取消、跳过或 provider 中断都必须形成与 tool-call ID 对应的结构化结果，避免产生无法重放的悬空调用。OMP 使用 synthetic result 区分未执行调用；Tau 将未知、异常和取消结果化（OMP `packages/agent/src/agent-loop.ts:2142-2164`；Tau `src/tau_agent/loop.py:190-235`）。
4. **typed event stream 是调用方 seam**：运行、turn、message、tool execution 和错误均有独立生命周期；渲染、HTTP/SSE、日志、指标和持久化由消费者完成（OMP `packages/agent/src/types.ts:701-727`；Tau `src/tau_agent/events.py:14-134`）。
5. **外部 append-only session 与 replay**：durable history 使用 typed entries 和 parent/leaf 路径；active context 由选定路径重建。compaction 改变重建出的 context，不删除原始历史（OMP `packages/agent/src/compaction/entries.ts:4-142`、`packages/agent/src/compaction/compaction.ts:1-6`；Tau `src/tau_agent/session/entries.py:15-114`、`src/tau_agent/session/memory.py:21-137`）。这些能力属于 Runtime 外的 session seam。

### 有条件迁移

- **Context compaction**：迁移 token-aware cut、保留近期消息、摘要替换和失败回退等抽象；不照搬模型窗口数值、coding 摘要 prompt 或文件操作摘要。
- **工具治理与调度**：OMP 的执行前后 hook、参数变换、partial update 与 shared/exclusive 调度，以及 Tau 的顺序执行/失败隔离，都可作为后续工具模型输入；具体权限、人工确认和副作用策略仍需 PiMoe 自行决定。
- **运行中输入队列**：steering/follow-up 的注入时机具有通用价值，但队列属于持续交互宿主状态；不能隐式放进已决定的无状态 Runtime。
- **Provider cache/dialect**：OMP 的 stable prefix、append-only cache 和 in-band tool dialect 只是 provider 优化或兼容适配，不是 Runtime 正确性的核心要求。

### 明确排除

不迁移 cwd/workspace discovery、read/write/edit/bash、文件操作摘要、coding-assistant prompt、TUI/CLI 交互、terminal breadcrumb、本地 session 目录和树导航命令。它们属于 OMP/Tau 的 coding-agent 宿主，而不是通用后端 Agent Runtime。

### 对后续决策的约束

- Context ticket 应围绕“强语义 Agent context → 明确变换 → provider projection”，并把 compaction 视为显式、可重放的 context 变换。
- Tool ticket 必须保留调用/result 配对、结构化失败、生命周期事件和可治理执行 seam。
- Memory ticket 必须严格区分 append-only transcript、compaction summary 与真正的长期记忆；OMP/Tau 没有提供可直接照搬的通用长期向量记忆模型。
