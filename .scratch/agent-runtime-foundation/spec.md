# Agent Runtime v1 规格

Status: resolved

## Problem Statement

PiMoe 当前已经能执行 Provider 流、tool-calling、JSONL transcript、managed session 和 HTTP/SSE 输出，但 Agent 执行层仍把状态、上下文、工具治理和 Provider 装配混在较窄的早期实现中。它缺少可复用的上下文预算策略、显式 compaction seam、长期记忆输入/候选 seam、工具审批与安全结果契约，导致后续业务型 Agent 只能复制 session、tool 和错误处理逻辑。

本规格不实现业务系统；目标是建立一个可被未来业务适配层复用的通用后端 Agent Runtime，并保留 Session 对 transcript 与持久化的所有权。

## Solution

实现 Runtime-to-Session 的最小可验证闭环：

- Agent Runtime 只执行一次不可变、已授权的 Run，并以 typed event stream 输出过程事实和恰好一个终态。
- Session 负责 transcript、恢复、同一会话 turn 互斥、取消句柄，以及成功提交/失败回滚。
- Application Service 负责 actor、业务授权、运行配置、Allowed Tool 选择和 MemoryStore 查询，不把这些职责下沉到 Runtime。
- Runtime 在每次 Provider 调用前准备 Context，使用模型能力和可替换估算器执行完整性检查、预算裁剪和显式 compaction。
- Runtime 只执行 request-scoped Allowed Tool；统一处理参数校验、审批、超时、结构化结果、tool-call/result 配对和最小审计。
- Runtime 消费调用方已授权的 MemoryItem，并可通过注入式 MemoryExtractor 提出带 provenance 的 MemoryCandidate；外部调用方决定是否提交。

## User Stories

1. 作为 Runtime 调用方，我希望传入不可变的 RunRequest，以便一次运行不依赖隐式 session 或全局可变状态。
2. 作为 Runtime 调用方，我希望收到稳定的 typed event stream，以便构建 HTTP、SSE、CLI、日志和其他消费端而不读取 Runtime 内部状态。
3. 作为 Runtime 调用方，我希望每次运行恰好有一个 completed、failed 或 canceled 终态，以便可靠提交或回滚外部状态。
4. 作为 Session，我希望把已恢复的 Conversation Transcript 交给 Runtime，以便 Runtime 不需要读取 SessionStore。
5. 作为 Session，我希望在成功 Run 后提交新增 transcript，以便失败和取消不会污染 durable history。
6. 作为 Session，我希望同一 Session 的 turn 保持互斥，以便并发请求不会覆盖 transcript leaf。
7. 作为调用方，我希望为 Run 提供基础 instructions、调用方约束和当前用户输入，以便这些不可删除内容始终保留。
8. 作为调用方，我希望提供已授权的 MemoryItem，以便 Runtime 使用业务相关上下文而不依赖具体 MemoryStore。
9. 作为调用方，我希望 MemoryItem 被当作不可信数据而非指令，以便旧记忆不能覆盖安全约束或提升工具权限。
10. 作为调用方，我希望 Context module 在每次 Provider 调用前重新估算预算，以便 tool loop 增长后仍不会盲目发送超限请求。
11. 作为调用方，我希望 Runtime 使用模型声明的 context window、输出 reserve 和可替换 TokenEstimator，以便不同 Provider 不共享错误的固定窗口。
12. 作为调用方，我希望上下文裁剪以完整 turn 和 tool-call/result group 为单位，以便不会生成悬空 tool call。
13. 作为调用方，我希望超限时先裁剪低优先级历史，再调用显式 ContextCompactor，以便上下文变化可解释且不会静默丢失强制内容。
14. 作为 Session，我希望 Context Summary 以候选形式返回，以便只有成功 Run 才能决定是否写入 append-only history。
15. 作为调用方，我希望 compaction 失败产生明确错误终态，以便系统不会在未知上下文下继续执行。
16. 作为 Agent Runtime，我希望只看到 request-scoped Allowed Tool，以便模型不会发现或执行当前 Run 未授权的能力。
17. 作为 Application Service，我希望根据 actor、Agent 类型和业务策略选择 Allowed Tool，以便业务授权留在正确的应用 seam。
18. 作为 Tool executor，我希望通过不透明 request-scoped capability 获得最小权限，以便凭据和业务连接细节不会进入模型上下文。
19. 作为 Tool owner，我希望声明工具的 schema、side-effect、concurrency 和 timeout policy，以便 Runtime 能一致治理不同工具。
20. 作为业务系统，我希望副作用 Tool 在执行前经过 Approval Gate，以便人工或策略确认成为统一事件流的一部分。
21. 作为调用方，我希望 Approval Gate 在当前 Run 内等待并受 context 取消，以便不引入跨请求 suspended 状态。
22. 作为模型，我希望工具失败获得安全、短小的 model content，以便继续推理而不接收凭据、内部 URL、响应体或堆栈。
23. 作为可信审计消费者，我希望获得独立的 internal details/error 和最小调用元数据，以便排查拒绝、超时、取消和真实失败。
24. 作为 Provider 适配层，我希望每个 assistant tool-call 都有配对结果，以便任何失败、取消、拒绝或未执行调用都能安全重放。
25. 作为调用方，我希望默认工具执行串行、exclusive 且不自动重试，以便副作用操作不会因隐式并发或重复执行产生风险。
26. 作为只读工具 owner，我希望显式声明 shared 且线程安全后并行执行，以便安全获得查询吞吐提升。
27. 作为调用方，我希望 MemoryExtractor 默认关闭，以便 Agent 不会未经配置自动收集用户信息。
28. 作为调用方，我希望 MemoryExtractor 只为稳定、有 provenance 的事实提出候选，以便模型猜测、临时状态和 reasoning 不会变成长期记忆。
29. 作为调用方，我希望 MemoryCandidate 只有在 completed Run 后才可提交，以便失败或取消的运行不会写入错误事实。
30. 作为 MemoryStore owner，我希望使用稳定 key、版本化 upsert、expiry 和 tombstone，以便记忆更新和遗忘可审计且不会从缓存中复活旧值。
31. 作为 HTTP/SSE 客户端，我希望只看到安全结果和非敏感状态，以便内部工具详情、记忆正文和 context 原文不会泄露。
32. 作为 Runtime 维护者，我希望 contract tests 覆盖终态、预算、工具配对、审批、超时、取消和错误，以便行为变更可以被快速定位。
33. 作为 Session 维护者，我希望 integration tests 覆盖成功提交与失败回滚，以便 Runtime 与持久化边界不会漂移。
34. 作为项目维护者，我希望核心验收依赖 fake provider 而非真实网络，以便测试快速、确定且可重复。
35. 作为项目维护者，我希望真实 Provider 只作为额外 smoke test，以便模型差异不会阻塞 Runtime contract 验收。

## Implementation Decisions

- Runtime 是无持久状态的单次执行 module；唯一公开运行语义是流式事件。
- RunRequest 是不可变、已授权的输入，包含 conversation、instructions、Allowed Tool、MemoryItem、模型能力与运行级依赖。
- Runtime 不接收 Session ID，不访问数据库、MemoryStore 或业务 API，不解释 actor、tenant 或业务 scope。
- 事件流包含 run/turn/message/tool/context/memory 生命周期事件，并且必须以一个显式终态结束。
- Session 是 Runtime 的有状态调用方，负责 transcript 恢复、turn 互斥、取消、成功提交和失败回滚。
- Application Service 负责 HTTP/CLI 之外的应用用例编排，包括 actor、业务授权、Allowed Tool 选择、capability 创建和 MemoryStore 查询。
- Context pipeline 为：强语义 Agent context、优先级与完整性校验、预算估算、裁剪/压缩、Provider projection。
- 强制 instructions 和当前用户输入不可删除；tool-call/result group 不可拆分；旧 turns 和低价值 tool results 优先被裁剪。
- Provider/model 必须提供 context window；TokenEstimator 可精确或 approximate，但必须标记估算类型并保留 safety margin。
- ContextCompactor 是可替换 seam；v1 提供一个 LLM compactor，可使用独立 model/provider。一次准备最多调用一次，失败产生明确 context failure。
- Context Summary 只作为本次 PreparedContext 使用；Session 在成功后决定是否写入 append-only compaction entry。
- Allowed Tool 至少包含稳定 name/version、description、JSON Schema、side-effect/concurrency policy、timeout policy 和 executor。
- executor 使用 request-scoped opaque capability；凭据和业务连接细节不得进入 schema、model message、transcript 或通用事件。
- Approval Gate 是 request-scoped 依赖；拒绝或缺失时不执行工具，而是生成 denied Tool Result。
- 工具默认串行、exclusive、无自动重试；只有显式 read-only/shared 且线程安全的工具才允许并行；重试必须由明确幂等策略启用。
- Effective timeout 是 Run deadline 与 Tool policy timeout 的较短者。
- Tool Result 区分 success、error、denied、timeout、canceled、skipped，并分离 model content 与 internal details/error。
- 未知工具、参数错误、审批拒绝、executor 异常、超时、取消和未执行调用都必须形成配对结果。
- 审计至少包含 run_id、tool_call_id、tool name/version、授权/审批决定、status、开始/结束时间及参数/输出 digest；默认不记录 secrets、完整凭据、原始响应或完整参数正文。
- Runtime 消费已授权 MemoryItem，但不实现 MemoryStore、向量检索或 scope 解释。
- MemoryExtractor 默认关闭，通过注入式 seam 提出带 provenance 的 upsert/forget MemoryCandidate；Extractor 失败不改变已经完成的主 Run 终态。
- Long-term Memory 使用稳定 key、版本化 upsert、expiry 和 tombstone；Conversation Transcript、Context Summary 和 Long-term Memory 不共享生命周期。
- 最高测试 seam 是 Runtime.Run contract seam；唯一集成 seam 是 Session.RunTurn。Handler/router 仅保留已有协议转换职责，不纳入本 spec 的核心行为验证。
- 现有 Session 对 Provider/Tool Registry 的隐式装配应迁移为注入 Runtime 或 RuntimeFactory，以保持 Session 的 transcript locality。

## Testing Decisions

- 测试只验证外部行为、事件契约、状态提交与错误分类，不测试私有函数调用次数、内部字段布局或具体实现类。
- Runtime contract tests 覆盖：事件顺序、终态 exactly-once、RunRequest 不变、context priority、mandatory overflow、TokenEstimator 标记、compaction 成功/失败、tool-call/result 配对、未知工具、参数错误、Approval Gate、timeout、cancellation 和脱敏。
- Session integration tests 覆盖：成功 Run 提交 transcript；failed/canceled Run 回滚；Context Summary 仅在成功后可提交；MemoryCandidate 仅在成功后交给提交器；同一 Session turn 串行。
- Fake-provider E2E 覆盖：纯文本 Run、至少一轮 tool loop、预算触发 compaction、审批拒绝、timeout/cancel，以及 provider error。
- Real Provider smoke test 只验证适配层连通和基本事件映射，不作为确定性核心验收门槛。
- 测试应复用现有 Agent loop、Session、stream contract 和 application service 测试风格；优先在 Runtime.Run 和 Session.RunTurn seam 验证完整行为。
- 所有测试必须可离线运行；测试凭据、memory content 和 tool output 使用固定 fixture，不能依赖真实业务系统。

## Out of Scope

- 具体业务 Tool、业务数据库、知识库摄取、向量数据库和 MemoryStore 实现。
- 用户认证、多租户隔离、业务 scope 解释和外部身份系统。
- 多 Agent 编排、计划器、后台自治任务和跨 Run suspended workflow。
- coding-agent 的 shell、filesystem、workspace、TUI、编辑器、CLI 专用工具和项目上下文发现。
- 真实 Provider 作为核心测试依赖。
- HTTP API 重新设计、前端开发和业务产品流程。
- 复杂 Provider dialect、prefix cache 和 provider-specific optimization；除非后续验证证明它们是必要适配。

## Further Notes

- 该规格来源于 `.scratch/agent-runtime-foundation/` 地图及其 6 张已解决决策 ticket。
- 参考 OMP/Tau 的研究资产位于 `.scratch/agent-runtime-foundation/research/`；只迁移 generic loop、context projection、tool-result pairing、append-only replay 和 compaction 抽象，不迁移 coding-agent 宿主机制。
- 实现应按 Runtime contract → Context → governed Tools → Session integration → Memory seam 的顺序拆解。
- 完成 spec 后再使用 `/to-tickets` 生成实现 tickets；不要直接把本 spec 当作一个大实现任务。
