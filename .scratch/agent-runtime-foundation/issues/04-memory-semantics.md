# 定义长期记忆语义与生命周期

Type: grilling
Status: resolved
Blocked by: 01, 03

## Question

区分可恢复 conversation transcript、上下文摘要和长期记忆。哪些事实可以写入长期记忆，由谁触发写入，按什么 scope 与生命周期检索，以及 Runtime 向调用方暴露怎样的小 interface 才能保持后续存储适配的 locality？

## Answer

PiMoe 明确区分三种状态：conversation transcript 是会话内完整运行事实；context summary 是为预算替换旧 transcript 的可重放视图；long-term memory 是可跨 run、并可按外部 scope 跨 session 检索的稳定事实。三者不能共用一个模糊的“memory”名称或生命周期。

### Runtime interface

Runtime 不依赖 `MemoryStore`，也不执行数据库或向量检索。最小 interface 只有两部分：

- 调用方完成 scope 解析、授权、检索、过滤和排序后，将 `[]MemoryItem` 作为不可变 `RunRequest` 的 context data 传入。
- Runtime 可以在事件流中产生 `MemoryCandidateEvent`，提出 `upsert` 或 `forget` 候选。候选只是建议；调用方仅在 run 以 `completed` 终态结束后，按自身策略确认、去重并提交外部 store。failed/canceled run 的候选不得持久化。

这样可以替换 MemoryStore、关键词/向量检索和业务授权实现，而不改变 Runtime interface。

### 可写入内容

只有稳定且有来源的事实允许成为候选：

- 用户明确陈述、预期跨 run 保留的偏好或约束。
- 由已授权工具确认的业务事实。
- 调用方或用户明确要求保留的决定。

每个候选必须携带 provenance，引用产生它的 transcript entry、tool execution 或调用方事实来源。模型猜测、未经确认的推断、临时任务状态、原始 reasoning、完整对话和 context summary 不能自动提升为长期记忆。

候选生成默认是显式策略能力，不因每次 run 自动开启。调用方可以按 Agent 类型启用或禁用，并可要求人工确认。

### Scope 与检索

- Scope 由调用方提供并解释，Runtime 只把它视为不透明 namespace/subject 标识，不内建 `user`、`tenant`、`session` 或 `global` 等业务层级。
- 调用方只能传入已授权 scope 下的 MemoryItem；Runtime 不跨 scope 检索或合并。
- MemoryItem 作为**不可信 context data**，不是 system/caller instruction，不能提升权限或授权工具。发生冲突时，当前明确用户输入和权威工具事实优先于旧记忆。
- 检索结果必须携带稳定 ID/key、scope、版本、provenance、更新时间、可选 expiry 与相关性元数据，才能参与 Context module 的优先级选择。

### 生命周期

- 一个逻辑事实使用稳定 key，更新采用版本化 upsert，不原地覆盖审计历史。
- 遗忘使用 tombstone；默认检索排除已 tombstone、过期和被新版本取代的条目，避免旧值从缓存或索引重新出现。
- MemoryStore 负责去重、版本冲突、expiry、tombstone 和索引一致性；Runtime 不实现这些存储策略。
- Context summary 不会自动成为 MemoryItem。只有满足上述写入条件、带 provenance 且经调用方策略确认后，summary 中的单个事实才可独立提升为长期记忆。

### 安全与可观察性

Memory candidate 内容属于敏感内部事件。可信的 session/业务适配层可以消费它以执行提交策略；HTTP/SSE 等外部适配默认只能暴露候选数量、ID、operation 和来源引用，不返回正文。日志和通用 context telemetry 同样只记录元数据。
