# 通用 Agent Runtime 基础能力地图

## Destination

形成 PiMoe 通用后端 Agent Runtime v1 的清晰能力边界、核心契约和按依赖排序的实现路线，使其能承载未来业务型 Agent；本地图完成于可以编写规格和实现任务时，而不直接交付业务系统。

## Notes

- 只讨论 `internal/agent`、`internal/session`、`internal/tools` 及其必要的 `internal/llms` 适配。
- 当前实现已有 Provider 流、tool-calling 循环、JSONL transcript、受管 session 和 HTTP/SSE 入口；不能把已有 transcript 持久化误称为长期记忆。
- 参考 `../oh-my-pi` 与 `../tau` 的可迁移机制，但不复制其 coding-agent 专用的终端、工作区、TUI 或编辑器耦合。
- 设计使用 `AGENTS.md` 的模块约束和 `codebase-design` 的 module、interface、seam、depth、leverage、locality 词汇。
- 本地图只产出决定；规格与实施任务在地图完成后另行生成。

## Decisions so far

- [定义 Agent Runtime 核心契约](issues/01-runtime-core-contract.md) — Runtime 只执行不可变 RunRequest 并输出单一 typed event stream；session 管理 transcript、持久化与同一会话的并发控制，取消仅经 context 传播。
- [审计 OMP 与 Tau 的可迁移 Agent 机制](issues/02-reference-pattern-audit.md) — 迁移无状态 loop、typed events、Agent context/provider 投影、tool-result 配对与外部 append-only replay；排除 workspace、文件/终端工具、TUI 和 coding 专用摘要。
- [定义上下文生命周期与预算策略](issues/03-context-lifecycle.md) — 每次 provider 调用前按分层优先级与原子工具组构造 PreparedContext；使用模型能力和可替换估算器，先裁剪再显式压缩，summary 由 session 在成功后决定是否持久化。
- [定义长期记忆语义与生命周期](issues/04-memory-semantics.md) — Runtime 只消费调用方已授权的 MemoryItem 并输出带 provenance 的候选；外部 store 负责不透明 scope、版本化 upsert、expiry 与 tombstone，记忆始终是不可信数据而非指令。
- [定义受治理的工具执行模型](issues/05-governed-tool-execution.md) — 调用方提供 request-scoped Allowed Tool 与 opaque capability；Runtime 统一执行审批、保守并发/超时、配对结果、双通道错误和最小审计，业务授权仍留在 Application Service。
- [确定 Agent Runtime v1 能力切口与验证路径](issues/06-v1-capability-cut.md) — v1 以 Runtime-to-Session tracer bullet 收敛 Run/Context、受治理 Tools 和 Memory seam；用 contract tests、Session integration、fake-provider E2E 验证，真实 Provider 仅作 smoke test。

## Not yet specified

当前地图没有未明确的前置决策；后续进入 `/to-spec` 和 `/to-tickets`。

## Out of scope

- 具体业务系统接入、知识库摄取、业务数据查询和业务 UI。
- 用户认证、多租户隔离和面向外部用户的产品能力。
- coding agent 的 shell、文件系统、代码编辑、TUI 或工作区执行能力。
- 多 Agent 编排、计划器和后台自治任务。
