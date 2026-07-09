# PostgreSQL 用户与 Session 存储设计

## 背景

当前项目已经具备 managed session、JSONL transcript、HTTP/CLI 入口和同 session run 串行化能力。当前 `SessionService` 直接持有 `session.Manager`，metadata 写入 `.moe/sessions/index.json`，transcript 写入 per-session JSONL 文件。系统尚未引入用户身份；所有 session 都等价于本地单用户资源。

后续目标是将用户信息、session metadata 和 transcript 逐步迁移到 PostgreSQL，并使用 GORM 作为数据库访问实现。为了避免一次性重写 runtime，本设计先定义用户归属和存储边界，再分阶段迁移。

## 目标

- 引入最小 `Actor` / `OwnerID` 语义，让 session API 从现在开始具备用户归属边界。
- 保持当前 CLI 和本地单用户体验不变，默认用户为 `local`。
- 抽出 session metadata store 边界，为后续 GORM/PostgreSQL 实现做准备。
- 使用 SQL migration files 管理 PostgreSQL schema；GORM model 只作为 storage implementation 的实现细节。
- 保留当前 JSONL transcript，后续用独立阶段迁移到 PostgreSQL `session_events`。

## 非目标

- 本阶段不实现注册、登录、JWT、OAuth、RBAC、团队、租户或 session sharing。
- 本阶段不把 Provider 配置、API Key 或 secret 入库；Provider registry 继续使用 YAML。
- 本阶段不迁移 transcript 到数据库。
- 本阶段不让 HTTP handler、CLI 或 service 层直接依赖 `*gorm.DB`、`gorm.Model`、`Preload` 或 GORM association。
- 本阶段不支持多实例分布式 session run 锁；当前仍以单进程内存锁为准。

## 领域模型

### Actor

`Actor` 表示当前请求的业务身份，不等同于认证方式。

```go
type Actor struct {
    UserID string
}
```

约定：

- `UserID` 不能为空；空值在入口处归一化为 `local`。
- CLI 和当前未认证 HTTP 默认使用 `Actor{UserID: "local"}`。
- 后续接登录时，由 HTTP middleware 从 cookie/token/header 解析 `Actor`，不改 `SessionService` 核心语义。

### Session ownership

`session.SessionMeta` 增加：

```go
OwnerID string
```

规则：

- 新建 session 必须写入 `OwnerID`。
- 当前 file store 新 session 默认 `OwnerID = actor.UserID`。
- 旧 index 中缺少 owner 的 legacy session 读取时视为 `local`。
- `List` 只返回当前 actor 拥有的 session。
- `Resolve` / `Get` / `Run` / `Stream` / `Touch` / `UpdateConfig` 必须校验 owner。

错误语义：

- 不存在 session 仍返回 not found。
- session 存在但 owner 不匹配时，也返回 not found 风格错误，避免向调用方泄露其他用户 session 是否存在。

## 服务层接口演进

当前：

```go
Create(ctx, title, opts...)
List(ctx)
Get(ctx, sessionID)
Run(ctx, sessionID, input, opts...)
Stream(ctx, sessionID, input, opts...)
```

演进为显式 actor：

```go
Create(ctx, actor, title, opts...)
List(ctx, actor)
Get(ctx, actor, sessionID)
Run(ctx, actor, sessionID, input, opts...)
Stream(ctx, actor, sessionID, input, opts...)
```

兼容策略：

- CLI/HTTP handler 先统一传入 `LocalActor()`。
- 不把 actor 隐式塞进 `context.Context`。显式参数更容易测试，也能防止权限边界在调用链中被忽略。

## Store 边界

第一阶段先保留现有 `session.Manager`，但目标接口如下：

```go
type SessionStore interface {
    Create(ctx context.Context, actor Actor, title string, cfg session.SessionConfig) (session.SessionMeta, error)
    Resolve(ctx context.Context, actor Actor, id string) (session.SessionMeta, error)
    List(ctx context.Context, actor Actor) ([]session.SessionMeta, error)
    UpdateConfig(ctx context.Context, actor Actor, id string, cfg session.SessionConfig) error
    Touch(ctx context.Context, actor Actor, id string) error
}
```

约束：

- owner 过滤必须在 store 边界体现。PostgreSQL 实现必须用 `where id = ? and owner_id = ?`，不能先查再由上层判断。
- file store 可以通过读取 index 后过滤实现，但语义必须与 PostgreSQL store 一致。
- transcript 路径暂时仍保留在 `SessionMeta.Path`，直到 transcript store 被独立抽出。

## PostgreSQL + GORM 边界

新增包建议：

```text
internal/storage/postgres
```

职责：

- 持有 `*gorm.DB`。
- 定义 GORM DB models。
- 实现 `SessionStore` / 后续 `UserStore`。
- 执行显式查询、事务和 mapper。

禁止：

- service/handler/CLI 直接 import GORM。
- 业务模型嵌入 `gorm.Model`。
- 依赖 association 自动保存复杂对象。
- 把 `*gorm.DB` 传出 storage 包。

GORM DB model 与业务 model 分开，例如：

```go
type UserRecord struct {
    ID          string  `gorm:"primaryKey;type:text"`
    Email       *string `gorm:"uniqueIndex;type:text"`
    DisplayName string  `gorm:"not null;type:text"`
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type SessionRecord struct {
    ID            string     `gorm:"primaryKey;type:text"`
    OwnerID       string     `gorm:"not null;type:text;index:idx_sessions_owner_updated,priority:1"`
    Title         string     `gorm:"not null;type:text"`
    ProviderName  string     `gorm:"type:text"`
    SessionPrompt string     `gorm:"type:text"`
    MaxSteps      int
    CreatedAt     time.Time
    UpdatedAt     time.Time   `gorm:"index:idx_sessions_owner_updated,priority:2,sort:desc"`
    ArchivedAt    *time.Time  `gorm:"index"`
}
```

需要 mapper：

```go
func sessionRecordToMeta(record SessionRecord) session.SessionMeta
func sessionMetaToRecord(meta session.SessionMeta) SessionRecord
```

## SQL migrations

生产 schema 使用 SQL migration files。GORM `AutoMigrate` 只允许用于测试或本地开发初始化，不作为生产 schema 变更来源。

建议目录：

```text
migrations/
  0001_users_sessions.up.sql
  0001_users_sessions.down.sql
```

第一批 schema：

```sql
create table users (
    id text primary key,
    email text unique,
    display_name text not null,
    created_at timestamptz not null,
    updated_at timestamptz not null
);

create table sessions (
    id text primary key,
    owner_id text not null references users(id),
    title text not null,
    provider_name text,
    session_prompt text,
    max_steps integer,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    archived_at timestamptz
);

create index sessions_owner_updated_idx
    on sessions(owner_id, updated_at desc);
```

默认用户：

```text
id = local
display_name = Local User
email = null
```

## Transcript 后续迁移

Transcript 不在第一批 PostgreSQL metadata PR 中迁移。后续使用 event 模型贴合当前 JSONL append-only 语义：

```sql
create table session_events (
    id text primary key,
    session_id text not null references sessions(id) on delete cascade,
    parent_id text,
    type text not null,
    payload jsonb not null,
    created_at timestamptz not null
);

create table session_leaf (
    session_id text primary key references sessions(id) on delete cascade,
    event_id text not null references session_events(id),
    updated_at timestamptz not null
);
```

语义保持：

- message/event append-only。
- 只有完整 run 的 `RunEnd` 后推进 leaf。
- 失败或取消 run 不推进 durable leaf。
- 恢复 transcript 时沿 leaf parent chain 读取。

## 同 session 并发

当前 `SessionService` 的 `sessionRunLocks` 是单进程内存锁。它只串行化同一个 session 的 `Run` / `Stream`，不同 session 可以并行。

PostgreSQL metadata 阶段仍保留该设计。多实例部署时再改为 PostgreSQL advisory lock 或 session run lease 表，例如：

```sql
select pg_advisory_xact_lock(hashtext($1));
```

本设计不提前实现分布式锁，避免为当前单实例过度设计。

## 分阶段实施

### PR 1：Actor + ownership boundary

- 新增 `Actor` 和 `LocalActor()`。
- `SessionMeta` / index entry 增加 `OwnerID`。
- 创建 session 写入 owner。
- list/resolve/get/run/stream/touch/update config 校验 owner。
- legacy empty owner 兼容 local。
- CLI/HTTP 入口传 `LocalActor()`。
- 测试 same owner、different owner、legacy local、missing session。

### PR 2：SessionStore interface extraction

- 抽出 `SessionStore`。
- 当前 file manager 实现该接口。
- `SessionService` 依赖接口而不是具体 manager。
- 保持 JSONL transcript 不变。

### PR 3：GORM/PostgreSQL users + sessions metadata store

- 新增 SQL migration files。
- 新增 GORM records 和 mapper。
- 新增 `GormSessionStore` / `GormUserStore`。
- 只迁 metadata，不迁 transcript。
- Provider config 继续 YAML。

### PR 4：PostgreSQL transcript/events

- 新增 `session_events` / `session_leaf` schema。
- 实现 append-only transcript store。
- 保持 RunEnd leaf 推进和 failed run rollback 语义。

## 验证策略

PR 1：

- `go test ./internal/session -count=1`
- `go test ./internal/application/service -count=1`
- CLI session 创建和 resume 相关测试。

PR 2：

- store contract tests 覆盖 file implementation。
- `go test ./internal/session ./internal/application/service -count=1`

PR 3：

- PostgreSQL store 使用测试数据库或 transaction rollback 测试。
- migration up/down 冒烟。
- 权限查询必须覆盖 same owner / different owner。

PR 4：

- transcript append/load/leaf 恢复 contract tests。
- failed run 不推进 leaf。
- 并发 same-session run 保持串行。

## 风险与决策

- 显式 actor 参数会改动多个调用点，但权限边界清晰，优于把 user 隐藏在 context 中。
- 先做 ownership 再迁数据库，避免把未定领域关系固化到表结构。
- GORM 限定在 storage 包内，避免 ORM 模型污染业务层。
- transcript 后迁，避免 metadata 迁移和 run/leaf 语义重写混在同一个 PR。
- 当前内存锁不支持多实例；在真正部署多实例前必须补 PostgreSQL advisory lock 或 lease 表。
