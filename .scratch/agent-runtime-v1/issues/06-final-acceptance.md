# 06 — 完成 Runtime-to-Session 验收闭环

**What to build:** 用离线、快速、确定的 fake-provider 场景证明 Runtime v1 能从 Runtime.Run 贯通到 Session，并保留一个可选真实 Provider smoke test。

**Blocked by:**
- 04 — 让 Session 注入并提交 Runtime Run
- 05 — 接入 MemoryItem 与 MemoryCandidate

**Status:** resolved

- [x] 覆盖纯文本 Run、至少一轮 tool loop、预算触发 compaction、审批拒绝、timeout/cancel 和 provider error。
- [x] 覆盖成功 transcript/summary 提交、失败/取消回滚和 memory candidate 提交边界。
- [x] 所有核心验收不依赖网络、真实模型或真实业务系统。
- [x] 运行 Runtime contract tests、Session integration tests 和 fake-provider E2E。
- [x] 真实 Provider smoke test 仅作为额外连通性检查，不成为确定性门禁。
- [x] 清理或迁移旧的重复装配路径，并记录最终验收结果。

## 验收记录

- `go test ./internal/agent ./internal/session ./internal/application/service ./cmd/cli`：Runtime、Session、service 与 CLI fake-provider 验收通过。
- `go test ./...`：全部离线测试通过；真实 Provider smoke 默认跳过。
- `go test -race ./internal/agent ./internal/session`：并发检查通过。
- `go vet ./...`：静态检查通过。
- `TestRealProviderRuntimeSmoke` 仅在设置 `PIMOE_REAL_PROVIDER_CONFIG` 时运行，可用 `PIMOE_REAL_PROVIDER_NAME` 覆盖默认 Provider；fake Provider 会被拒绝。
- `NewConfiguredRuntime` 直接组装一次 Provider、工具 capability 和 Runtime，不再先创建并丢弃一个旧 Agent。
