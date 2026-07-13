# 02 — 实现 Context 预算与 Compaction

**What to build:** 让一次 Run 在每次 Provider 调用前生成可解释、预算受控且不会破坏 tool-call/result 配对的 PreparedContext。

**Blocked by:** 01 — 建立 Runtime Run 与事件终态契约

**Status:** resolved

- [x] 在每次 Provider 调用前重新估算 Context。
- [x] 支持 model context window、output reserve、可替换 TokenEstimator 和 approximate 标记。
- [x] 保留强制 instructions、当前用户输入和完整 tool-call/result group。
- [x] 按优先级裁剪完整 turns，不进行单条消息的静默截断。
- [x] 注入 ContextCompactor，并限制一次准备最多调用一次。
- [x] 输出 context metadata；mandatory overflow 和 compaction failure 形成明确失败终态。
- [x] 覆盖预算、裁剪、压缩成功、压缩失败和敏感正文不进入通用事件的测试。
