# 04 — 让 Session 注入并提交 Runtime Run

**What to build:** 让一个已有 Session 能通过注入的 Runtime 完成 turn，并根据唯一终态正确提交或回滚 transcript 与 Context Summary。

**Blocked by:**
- 02 — 实现 Context 预算与 Compaction
- 03 — 实现受治理 Tool 执行

**Status:** resolved

- [x] Session 通过注入 Runtime 或 RuntimeFactory 执行 Run，不再隐式拥有 Provider/Tool Registry 装配。
- [x] 恢复 Conversation Transcript 并组装 RunRequest 所需的调用方输入。
- [x] 保持同一 Session turn 互斥和 context 取消。
- [x] completed 后提交新增 transcript 和被接受的 Context Summary。
- [x] failed/canceled 后回滚本轮临时状态，不推进 durable context。
- [x] 通过 Session 集成测试覆盖成功、失败、取消和并发 turn。
