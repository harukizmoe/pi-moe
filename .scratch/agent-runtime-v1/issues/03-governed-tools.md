# 03 — 实现受治理 Tool 执行

**What to build:** 让一次 Run 只能执行调用方授权的工具，并统一处理审批、超时、结果配对、错误脱敏和审计。

**Blocked by:** 01 — 建立 Runtime Run 与事件终态契约

**Status:** resolved

- [x] 支持 request-scoped Allowed Tool、稳定 schema/version 和 opaque execution capability。
- [x] 未知工具、未授权工具和无效参数不调用 executor，而生成配对结果。
- [x] 支持 request-scoped Approval Gate；拒绝或缺失时生成 denied Tool Result。
- [x] 默认串行、exclusive、无自动重试；只读 shared 工具必须显式声明。
- [x] 合并 Run deadline 与 Tool timeout，覆盖 timeout、canceled 和 skipped。
- [x] 将 model content 与 internal details/error 分离，并禁止敏感原文进入模型 transcript。
- [x] 发出最小审计元数据并覆盖所有 Tool Result 状态。
