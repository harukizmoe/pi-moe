# 05 — 接入 MemoryItem 与 MemoryCandidate

**What to build:** 让调用方可向 Run 提供已授权的 MemoryItem，并在显式启用时获得带 provenance 的长期记忆候选，而不把 MemoryStore 耦合进 Runtime。

**Blocked by:** 04 — 让 Session 注入并提交 Runtime Run

**Status:** resolved

- [x] RunRequest 接收 MemoryItem，并将其作为不可信 context data 而非 instruction。
- [x] 提供注入式 MemoryExtractor，默认关闭。
- [x] 提供一个可替换的 LLM extractor 实现，候选只包含稳定、有 provenance 的事实。
- [x] 输出 upsert/forget MemoryCandidateEvent，并保留来源和 scope 元数据。
- [x] 仅 completed Run 的候选交给外部提交器；failed/canceled 候选不可提交。
- [x] extractor 失败不改变已经完成的主 Run 终态。
- [x] 不引入 MemoryStore、向量数据库或业务 scope 解释。
