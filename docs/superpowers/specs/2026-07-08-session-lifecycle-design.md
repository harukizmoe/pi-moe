# Session Lifecycle Manager 设计

## 背景

当前项目已经进入 `config -> llms -> agent -> tools -> session -> CLI` 的最小闭环阶段。`internal/session` 已具备两种入口：`New(ctx, cfg)` 创建纯内存 Session，`Open(ctx, cfg, path)` 从显式 JSONL 文件恢复 transcript。CLI 也已经有低层 escape hatch：`--session <path>`。

这个能力仍然偏工程调试：用户必须自己管理 session 文件路径，无法查看已有会话，也没有“新建会话 / 恢复会话 / 当前会话”的稳定入口。下一步应补齐最小 session 生命周期管理，而不是直接进入 HTTP API、database、memory、branch 或 compaction。

## 目标

1. 增加一个轻量 Session Manager，负责管理 `.moe/sessions` 下的会话索引和文件路径。
2. CLI 支持创建、恢复、列出本地 session，而不是只靠手写 `--session <path>`。
3. 保留现有 `--session <path>` 作为底层显式路径入口，保证调试和测试仍可直接指定文件。
4. 保持 `internal/session.Session` 的职责不变：它仍只负责 runtime transcript、Prompt、Cancel、Events、Messages 和 JSONL-backed Open。
5. 不引入数据库，不改变 Agent/LLM/Tools 的公开契约。

## 非目标

- 不实现 branch、compaction、memory、自动摘要或 session 搜索。
- 不引入 HTTP API、RPC、TUI 或浏览器 UI。
- 不把 session index 写进 provider/config/logger 包。
- 不改变 JSONL transcript entry schema；manager 只保存索引元数据。
- 不自动迁移用户手写的任意 `--session <path>` 文件到索引。

## 设计

### 总体边界

新增 manager 位于 `internal/session` 包内，原因是它管理 session 文件和 `Session.Open` 的入口关系，但不参与 Agent run。它不应该调用 LLM，不应该知道 tool schema，也不应该解析 transcript 内容。

职责分层：

```text
cmd/cli
  解析 --new-session / --resume / --list-sessions / --session
  调用 session.Manager resolve 出 session 文件路径
  调用 session.Open 或 session.New

internal/session.Manager
  管理 .moe/sessions/index.json
  创建 session id 和 session 文件路径
  维护 current / created_at / updated_at / title
  不执行 Prompt，不解析 transcript JSONL

internal/session.Session
  持有 Agent runtime
  Prompt/Cancel/Events/Messages
  Open JSONL transcript
```

### 文件布局

默认本地目录：

```text
.moe/
  sessions/
    index.json
    20260708-150405-a1b2c3.jsonl
    20260708-151210-d4e5f6.jsonl
```

`index.json` 是唯一索引文件，保存本地 session 元数据：

```json
{
  "current": "20260708-150405-a1b2c3",
  "sessions": [
    {
      "id": "20260708-150405-a1b2c3",
      "path": ".moe/sessions/20260708-150405-a1b2c3.jsonl",
      "title": "use calculator to compute 13 * 7",
      "created_at": "2026-07-08T15:04:05Z",
      "updated_at": "2026-07-08T15:04:05Z"
    }
  ]
}
```

字段语义：

- `current`：最近创建或恢复的 session id；第一版只用于默认 resume 候选，不自动执行。
- `id`：稳定 session 标识，格式为 UTC 时间戳加短随机/递增后缀。
- `path`：该 session 的 JSONL transcript 文件路径。
- `title`：创建时用首个 prompt 的裁剪文本生成，最多保留一行短文本；空 prompt 场景使用 `untitled session`。
- `created_at`：manager 创建该 session 的时间。
- `updated_at`：每次 CLI 使用该 session 完成一次 run 后更新。

### Manager API

新增 API 草案如下，实施时保持这些职责和语义，命名只允许因现有 Go 约定做小幅调整：

```go
// Manager 管理本地 session index 和 session 文件路径。
type Manager struct {
    root string
}

// SessionMeta 描述一个可恢复的本地 session。
type SessionMeta struct {
    ID        string
    Path      string
    Title     string
    CreatedAt time.Time
    UpdatedAt time.Time
}

// NewManager 创建使用 root 目录的 session manager。
func NewManager(root string) *Manager

// Create 创建一条索引记录并返回可传给 Open 的 session 文件路径。
func (m *Manager) Create(ctx context.Context, title string) (SessionMeta, error)

// Resolve 根据 id 返回已有 session 元数据。
func (m *Manager) Resolve(ctx context.Context, id string) (SessionMeta, error)

// List 按 updated_at 倒序返回 session 列表。
func (m *Manager) List(ctx context.Context) ([]SessionMeta, error)

// Touch 更新 session 的 updated_at，并将它设为 current。
func (m *Manager) Touch(ctx context.Context, id string) error
```

第一版不提供删除、重命名、搜索。需要改 title 时后续再加 `Rename`，不要提前做。

### CLI 语义

保留现有入口：

```text
--session <path>
```

它仍然直接调用 `session.Open(ctx, cfg, path)`，不读写 `index.json`。

新增入口：

```text
--new-session
--resume <session-id>
--list-sessions
```

语义：

- `--new-session "prompt"`：
  1. 用 prompt 生成 title。
  2. Manager 创建 session id 和 path。
  3. CLI 调用 `session.Open(ctx, cfg, meta.Path)`。
  4. run 成功后调用 `Manager.Touch(ctx, meta.ID)`。

- `--resume <session-id> "prompt"`：
  1. Manager 从 index 中解析 id。
  2. CLI 调用 `session.Open(ctx, cfg, meta.Path)`。
  3. run 成功后调用 `Manager.Touch(ctx, meta.ID)`。

- `--list-sessions`：
  1. Manager 读取 index。
  2. CLI 输出稳定文本列表。
  3. 不创建 Session，不调用 Provider，不要求 API Key。

- `--interactive --new-session`：
  1. 创建一个 session。
  2. 本进程内多轮 prompt 共用同一个 `Session`。
  3. 交互循环正常结束后 `Touch` 一次即可。

- `--interactive --resume <id>`：
  1. 恢复已有 session。
  2. 交互循环内继续复用同一个 `Session`。
  3. 退出时 `Touch`。

冲突规则：

- `--session` 与 `--new-session` / `--resume` 互斥。
- `--new-session` 与 `--resume` 互斥。
- `--list-sessions` 不能与 prompt、`--interactive`、`--session`、`--new-session`、`--resume` 同时使用。
- 未传任何 session 相关 flag 时保持当前纯内存行为。

### 输出格式

`--list-sessions` 输出一行一条，稳定且便于测试：

```text
20260708-150405-a1b2c3  2026-07-08T15:04:05Z  use calculator to compute 13 * 7
```

第一列是 id，第二列是 `updated_at` 的 UTC RFC3339，后面是 title。不要加表格边框，不引入颜色。

### 错误处理

- index 文件不存在：`List` 返回空列表；`Resolve` 返回明确的 not found 错误。
- index JSON 损坏：返回带路径上下文的错误，不能静默重建。
- session id 不存在：CLI 返回错误，不自动创建新 session。
- 创建目录失败：返回 wrapped error。
- `Touch` 时发现 id 不存在：返回错误，避免误写 current。

### 测试策略

Session manager 层：

- `Create` 创建 index 和 session path，title 被裁剪并可为空兜底。
- `Resolve` 能按 id 找回 meta。
- `List` 按 `updated_at` 倒序。
- malformed `index.json` 返回明确错误。
- `Touch` 更新 `updated_at` 和 `current`。

CLI 层：

- `parseCLIOptions` 接受 `--new-session`、`--resume`、`--list-sessions`。
- 冲突 flag 返回解析或校验错误。
- `--list-sessions` 不调用 Provider。
- `--new-session` 后能用 `--resume <id>` 复用 transcript。
- 现有 `--session <path>` 行为不变。

所有测试使用 fake provider 和临时目录，不依赖真实网络、真实 API key 或用户 `.moe` 目录。

## 风险

- 如果 CLI 自己拼 session 路径，会让路径规则分散；必须由 Manager 统一生成和解析。
- 如果 index 损坏时自动重建，会掩盖用户数据问题；必须显式报错。
- 如果 `--list-sessions` 需要加载 Provider 配置，会让只读命令依赖 API key；必须避免。
- 当前工作区已有未提交 session resume 实现；执行本设计前应先提交或明确隔离，避免两个阶段混在一个 commit。
