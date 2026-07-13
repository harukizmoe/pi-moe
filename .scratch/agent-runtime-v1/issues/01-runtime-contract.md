# 01 — 建立 Runtime Run 与事件终态契约

**What to build:** 让调用方能够提交一次不可变 Run，并通过稳定 typed event stream 观察完整生命周期和唯一终态。

**Blocked by:** None — can start immediately.

**Status:** resolved

- [x] 定义不可变 RunRequest，Runtime 不读取 Session ID、数据库或全局可变状态。
- [x] 提供单一 Runtime.Run 流式入口和稳定生命周期事件。
- [x] 保证每次运行恰好产生 completed、failed 或 canceled 之一。
- [x] 保证输入快照不被 Runtime 修改，取消只通过 context 传播。
- [x] 用 fake provider 覆盖纯文本 Run、provider error、提前取消和终态 exactly-once。
