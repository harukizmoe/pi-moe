# 定义上下文生命周期与预算策略

Type: grilling
Status: resolved
Blocked by: 01, 02

## Question

Runtime 如何组装一次模型调用的上下文，并在上下文预算接近上限时作出可验证的选择？确定 transcript、system prompt、session prompt、工具结果和未来检索结果的优先级；同时确定压缩、截断、摘要和失败行为的责任归属。

## Answer

Context management 是 Runtime 内部的单次运行准备 module，但不拥有 durable history。它在**每次 provider 调用前**从不可变 `RunRequest` 和本次 run 已产生的 assistant/tool-result 事实构造临时 `PreparedContext`；不修改调用方输入，也不直接写 session。

### 上下文管线

`AgentContext → priority/完整性校验 → budget estimate → prune/compact → provider projection`

- Agent 的强语义 message、instruction、memory/retrieval item 与 tool 定义先保留为 Runtime 类型；最后一步才投影为 provider wire message。
- Assistant tool-call 及其全部 tool result 是不可拆分的原子组。任何裁剪或压缩都不能产生悬空 tool call。
- Tool loop 每增加一批 assistant/tool-result 后，下一次 provider 调用都重新估算预算；不能只在 run 开始时检查一次。

### 保留优先级

从高到低：

1. 基础、安全和调用方约束型 instructions，以及当前用户输入；这些内容不可删除。
2. 本次 run 当前未完成的 assistant tool-call/result 链，以及 allowed tools 的 schema。
3. 与当前输入相关的检索结果和未来长期记忆；它们必须携带来源、scope 和相关性元数据，才能参与选择。
4. 最近的完整 conversation turns。
5. 已有 context/compaction summary。
6. 更旧的完整 turns 和低价值历史 tool results。

同一优先级内保持原始时序；不得按单条 message 随意截断。

### 预算来源

- Provider/model capability 必须声明 context window；调用方或模型配置同时给出输出 token reserve。Runtime 不使用一个全局固定窗口。
- Runtime 依赖可替换 `TokenEstimator` 估算 instructions、messages、tool schema 和预留输出。支持精确 tokenizer 时使用精确实现；否则使用明确标记为 approximate 的保守实现，并保留 safety margin。
- 若 mandatory 内容加输出 reserve 已超过窗口，立即以 `context_budget_exceeded` 失败，不尝试删除强制内容。

### 超限处理

1. 按优先级删除最低价值的**完整单元**，优先删除旧 turns 和旧 tool results。
2. 仍超限时，调用显式 `ContextCompactor`，将一段旧历史转换为带来源范围的 summary；一次准备过程不反复无限调用摘要模型。
3. 没有 compactor、compactor 失败、summary 仍超限或 provider 实际拒绝上下文时，以 `context_budget_exceeded` / `context_compaction_failed` 的 failed 终态结束；不静默截断，不隐式重试到结果改变。

Compactor 生成的 summary 只用于本次 `PreparedContext`。Runtime 通过事件返回 summary 候选、被替换范围和估算元数据；session 仅在 run 成功且接受该变换时，才把它作为 append-only compaction entry 持久化。失败或取消的 run 不推进 durable context。

### 可观察性与隐私

typed event stream 暴露结构化 context 元数据：模型窗口、输出 reserve、估算器类型及 exact/approximate 标记、各来源估算量、裁剪的 entry/turn 范围、是否执行 compaction、summary 引用和失败原因。

事件默认不包含 system instructions、memory、retrieval 文本或完整 `PreparedContext`，避免敏感上下文进入日志和 SSE。需要正文诊断时必须由受控调试适配层显式开启，而不是 Runtime 的默认事件契约。
