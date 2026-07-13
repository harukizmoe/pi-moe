# 确定 Agent Runtime v1 能力切口与验证路径

Type: grilling
Status: resolved
Blocked by: 03, 04, 05

## Question

基于已确定的 context、memory 与 tool 决定，v1 最小闭环包含什么、明确排除什么，并用哪些可重复的场景和质量门槛证明 Runtime 已能承载后续业务型 Agent？

## Answer

Agent Runtime v1 以一个可验证的 Runtime-to-Session tracer bullet 为目标，不包含业务系统接入。它必须把此前四张决策 ticket 的 interface 变成可运行闭环。

### v1 包含

1. **单次 Run 与 Context**：不可变 `RunRequest`、typed event stream、每次 provider 调用前的 context integrity/budget 检查，以及恰好一个 `completed`/`failed`/`canceled` 终态。包含模型 capability、可替换 `TokenEstimator`、可解释裁剪和结构化 context metadata。
2. **注入式 ContextCompactor**：ContextCompactor 是 request/runtime 外部可替换 seam；提供一个 LLM compactor 实现，可使用独立 model/provider。一次准备最多调用一次；失败不能静默改变输入，必须以明确 context failure 终态结束。summary 仍由 session 在成功后决定是否持久化。
3. **受治理 Tools**：request-scoped Allowed Tool、opaque execution capability、参数校验、Approval Gate、effective timeout、默认串行/exclusive、无自动重试、结构化双通道 Tool Result、tool-call/result 配对和最小审计事件。
4. **Memory seam**：RunRequest 接受调用方已授权的 MemoryItem；提供可注入 MemoryExtractor 的 interface 和一个 LLM extractor 实现。Extractor 默认关闭，候选带 provenance，以 MemoryCandidateEvent 输出；只有 completed Run 的候选可由调用方提交外部 MemoryStore。Extractor 失败不应让已经完成的主 Run 变成失败，只产生受控诊断事件。

### v1 不包含

- 具体业务 Tool、业务数据库、知识库、向量数据库和 MemoryStore 实现。
- 用户认证、多租户和业务 scope 解释。
- 多 Agent 编排、计划器、后台自治任务。
- coding-agent 的 shell、filesystem、workspace、TUI、编辑器和 CLI 专用能力。
- 真实模型作为核心验收依赖；真实 Provider 只做额外 smoke test。

### 实现顺序

1. 先收紧 Runtime request/event/terminal contract，确保终态恰好一次且输入不被修改。
2. 加入 Context module、TokenEstimator 和注入式 ContextCompactor。
3. 加入 Allowed Tool、Approval Gate、timeout、structured result 和 audit metadata。
4. 让 Session 注入 Runtime，验证 completed commit 与 failed/canceled rollback；移除 Session 对 Provider/Registry 装配的隐式拥有。
5. 接入 MemoryItem 和可注入 MemoryExtractor，验证候选只在成功后可提交。

### 验证门槛

- **Runtime contract tests**：事件顺序、终态 exactly-once、不可变 RunRequest、context priority、预算失败、compaction 失败、tool 配对、审批拒绝、超时、取消和未知工具。
- **Session integration tests**：成功 run 提交 transcript/summary；失败或取消回滚；memory candidate 只有成功后交给提交器；同一 Session 仍保持 turn 串行。
- **Fake-provider end-to-end**：至少覆盖纯文本 Run、tool loop、预算触发 compaction、审批拒绝和取消；核心门禁保持快速、确定，不依赖网络或真实模型。
- **Real-provider smoke**：作为额外检查，不作为确定性 v1 验收条件。

地图到此具备编写 spec 和实现 tickets 的全部前置决定；下一步进入 `/to-spec`，而不是继续在地图中直接实现。
