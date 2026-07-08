# Session Resume API 设计

## 背景

项目已经具备 JSONL transcript、session index、HTTP `create/list/run/stream/detail` 和 CLI `--new-session/--resume/--list-sessions` 基础。当前缺口不是新增更大的能力，而是把“创建 -> 运行 -> 关闭 -> 恢复 -> 继续 -> 读回”的会话闭环固定为可验证 contract。

## 目标

- HTTP 对同一个 session 连续运行多轮时，必须恢复已有 transcript 并追加新 turn。
- CLI `--resume <id>` 必须恢复 manager-managed session，并追加到同一个 JSONL 文件。
- `GET /v1/sessions/:id` 必须能读回多轮 terminal transcript，顺序稳定。
- `updated_at` 必须在 resume/run 成功后更新。
- missing session 必须在 HTTP 和 CLI 上暴露明确错误。
- fake provider 必须能确定性证明第二轮读到了历史，不依赖真实 Provider。

## 非目标

- 不新增 `POST /v1/sessions/:id/resume`。现有 `POST /v1/sessions/:id/runs` 已经表达“在该 session 上继续运行”。
- 不引入数据库。
- 不实现 memory、branch/tree 或 UI 聚合 API。
- 不改变 JSONL append-only 存储模型。

## 设计

### HTTP resume 语义

`POST /v1/sessions/:id/runs` 是唯一的 HTTP resume+append 入口：

1. 通过 session manager resolve metadata。
2. 用 metadata 中的 path 打开并恢复 JSONL transcript。
3. 将新 prompt 追加到同一 session。
4. 成功完成 run 后调用 `Touch` 更新 `updated_at`。
5. 后续 `GET /v1/sessions/:id` 返回完整 terminal transcript。

HTTP 不新增重复 endpoint，避免 `/runs` 和 `/resume` 语义分裂。

### CLI resume 语义

CLI 保持现有参数形态：

```bash
pimoe --new-session "use calculator to compute 13 * 7"
pimoe --resume <session-id> "what was the previous result?"
pimoe --list-sessions
```

要求：

- `--new-session` 创建 manager-managed session 并运行首轮 prompt。
- `--resume` 使用同一 session id 恢复 JSONL 并追加新 prompt。
- run 成功后更新 manager index 的 `updated_at`。
- `--list-sessions` 能看到更新后的时间。

测试使用临时 session root，不污染真实 `.moe/sessions`。

### Fake Provider 历史证明

当前 fake provider 能验证 tool-calling，但不足以证明第二轮看到了历史。增加一个确定性分支：

- 当最后一个 user message 包含 `previous result` 或 `上一轮结果`：
  - 如果历史中存在 tool result `91`，返回 `previous result was 91`。
  - 如果不存在，返回 `no previous result found`。
- 其他 prompt 继续保持现有 calculator tool-calling 行为。

这让测试能区分“真正恢复了历史”和“只处理当前 prompt”。

### 错误语义

新增最小 not-found 判断边界，避免 handler 通过字符串包含判断 HTTP 404：

- session/data/service 层提供 `IsNotFound(err error) bool` 或等价轻量函数。
- HTTP handler 使用该函数将 missing session 映射为 JSON 404。
- CLI 保留明确错误信息，包含 missing session id。

不引入复杂错误体系。

## 测试计划

- `internal/llms`: fake provider 覆盖有历史和无历史的 `previous result` 分支。
- `internal/application/service`: 同一 session 连续两轮 run，第二轮返回 `previous result was 91`，detail 返回两轮 transcript。
- `internal/application/router`: create -> run -> run same id -> GET detail，断言 200、消息顺序、第二轮答案、`updated_at` 增长；missing session 返回 JSON 404。
- `cmd/cli`: `--new-session` 后 `--resume` 同 id，断言同一 managed session 追加 transcript 且 `updated_at` 更新。
- smoke script 可追加最小 HTTP resume 检查；不要求覆盖 CLI。

## 验收标准

- fake provider 能确定性证明历史恢复。
- HTTP 同 session 多轮 run 后，detail 返回完整有序 transcript。
- CLI resume 追加到同一 session 文件，并更新 index。
- missing session 404 不依赖 handler 字符串匹配。
- 验证命令通过：

```bash
go test -count=1 ./internal/llms ./internal/application/service ./internal/application/router ./cmd/cli ./cmd/server
go test -race -count=1 ./...
bash -n scripts/test-sse.sh
```

## 风险

- fake provider 分支必须只服务测试契约，不能演变成业务逻辑。
- not-found 边界应保持小，不提前抽象错误分类框架。
- CLI 和 HTTP 共享 session root 的真实端到端行为后续可由 smoke 覆盖，本阶段先用临时 root 单元测试证明。