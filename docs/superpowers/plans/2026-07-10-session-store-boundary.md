# Session Store Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 抽出 `session.SessionStore` 边界，让应用服务依赖 store 接口而不是 file-backed `Manager` 具体类型，同时保持 CLI/HTTP 行为不变。

**Architecture:** `internal/session.Manager` 继续作为当前 file-backed store 实现，不重命名、不搬目录，避免大规模改动。新增 `SessionStore` 接口描述 metadata store 契约；`SessionService` 通过接口字段调用 store，默认仍由 `session.NewManager(SessionRoot)` 提供。

**Tech Stack:** Go、当前 file-backed session index、标准库测试；本计划不引入 GORM/PostgreSQL/migrations/auth/transcript 迁移。

---

## Scope

### In scope

- 在 `internal/session` 定义 `SessionStore` 接口。
- 让 `Manager` 显式满足 `SessionStore`。
- 让 `internal/application/service.SessionService` 依赖 `session.SessionStore`。
- 为 service 增加可注入 store 的构造配置，便于后续 PostgreSQL store 接入。
- 保持现有 CLI/HTTP 行为和公开响应不变。

### Out of scope

- 不新增 `internal/storage/postgres`。
- 不新增 SQL migration files。
- 不引入 GORM、PostgreSQL driver 或数据库配置。
- 不迁移 transcript；`SessionMeta.Path` 仍由 file-backed store 返回。
- 不实现 auth/JWT/user middleware。

---

## File Structure

- `internal/session/store.go`
  - 新增 `SessionStore` 接口。
  - 新增 `var _ SessionStore = (*Manager)(nil)` 编译期断言。
- `internal/session/manager.go`
  - 只更新 `Manager` 注释，使其说明自己是 file-backed `SessionStore` 实现。
- `internal/session/manager_test.go`
  - 增加接口契约烟雾测试，证明通过接口调用仍保持 owner 过滤语义。
- `internal/application/service/session.go`
  - `SessionService.manager` 改为 `store session.SessionStore`。
  - `SessionConfig` 增加 `Store session.SessionStore` 可选字段。
  - `NewSessionService` 默认使用 `session.NewManager(cfg.SessionRoot)`；传入 `Store` 时使用注入 store。
  - 所有 `s.manager.*` 改为 `s.store.*`。
- `internal/application/service/session_test.go`
  - 增加注入 fake store 的测试，证明 service 不依赖 `*session.Manager` 具体类型。

---

### Task 1: Define `SessionStore` in `internal/session`

**Files:**
- Create: `internal/session/store.go`
- Modify: `internal/session/manager.go`
- Modify: `internal/session/manager_test.go`

- [ ] **Step 1: Write failing interface contract test**

Add this test to `internal/session/manager_test.go`:

```go
func TestSessionStoreInterfaceUsesManagerOwnershipRules(t *testing.T) {
	var store SessionStore = NewManager(filepath.Join(t.TempDir(), "sessions"))
	ctx := context.Background()
	alice := Actor{UserID: "alice"}
	bob := Actor{UserID: "bob"}

	created, err := store.Create(ctx, alice, "alice prompt", SessionConfig{ProviderName: "fake"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.OwnerID != "alice" {
		t.Fatalf("Create() OwnerID = %q, want alice", created.OwnerID)
	}
	if _, err := store.Resolve(ctx, bob, created.ID); !IsNotFound(err) {
		t.Fatalf("Resolve() bob error = %v, want not found", err)
	}
	listed, err := store.List(ctx, alice)
	if err != nil {
		t.Fatalf("List() alice error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("List() alice = %#v, want created session", listed)
	}
}
```

- [ ] **Step 2: Run test to verify RED**

Run:

```bash
go test ./internal/session -run TestSessionStoreInterfaceUsesManagerOwnershipRules -count=1
```

Expected: FAIL to compile with `undefined: SessionStore`.

- [ ] **Step 3: Add `SessionStore` interface**

Create `internal/session/store.go`:

```go
package session

import "context"

// SessionStore 定义 session metadata 的持久化边界；实现必须在查询边界执行 owner 过滤。
type SessionStore interface {
	Create(ctx context.Context, actor Actor, title string, cfg SessionConfig) (SessionMeta, error)
	Resolve(ctx context.Context, actor Actor, id string) (SessionMeta, error)
	List(ctx context.Context, actor Actor) ([]SessionMeta, error)
	UpdateConfig(ctx context.Context, actor Actor, id string, cfg SessionConfig) error
	Touch(ctx context.Context, actor Actor, id string) error
}

var _ SessionStore = (*Manager)(nil)
```

Update the `Manager` comment in `internal/session/manager.go`:

```go
// Manager 是基于本地 index.json 的 file-backed SessionStore 实现。
type Manager struct {
	root string
	mu   sync.Mutex
}
```

- [ ] **Step 4: Run focused tests**

Run:

```bash
go test ./internal/session -run 'TestSessionStoreInterfaceUsesManagerOwnershipRules|TestManager(CreateResolveListFiltersByOwner|OwnerMismatchCannotTouchOrUpdateConfig|ReadsLegacyEmptyOwnerAsLocal)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/session/store.go internal/session/manager.go internal/session/manager_test.go
git commit -m "feat: define session store boundary"
```

---

### Task 2: Make `SessionService` depend on `SessionStore`

**Files:**
- Modify: `internal/application/service/session.go`
- Modify: `internal/application/service/session_test.go`

- [ ] **Step 1: Write failing injected-store test**

Add this fake and test to `internal/application/service/session_test.go` near existing service tests:

```go
type recordingSessionStore struct {
	created []session.SessionMeta
	actor   session.Actor
}

func (s *recordingSessionStore) Create(ctx context.Context, actor session.Actor, title string, cfg session.SessionConfig) (session.SessionMeta, error) {
	s.actor = session.NormalizeActor(actor)
	meta := session.SessionMeta{ID: "injected-session", OwnerID: s.actor.UserID, Path: filepath.Join(os.TempDir(), "injected-session.jsonl"), Title: title, Config: cfg}
	s.created = append(s.created, meta)
	return meta, nil
}

func (s *recordingSessionStore) Resolve(ctx context.Context, actor session.Actor, id string) (session.SessionMeta, error) {
	for _, meta := range s.created {
		if meta.ID == id && meta.OwnerID == session.NormalizeActor(actor).UserID {
			return meta, nil
		}
	}
	return session.SessionMeta{}, fmt.Errorf("session %q not found", id)
}

func (s *recordingSessionStore) List(ctx context.Context, actor session.Actor) ([]session.SessionMeta, error) {
	owner := session.NormalizeActor(actor).UserID
	out := make([]session.SessionMeta, 0, len(s.created))
	for _, meta := range s.created {
		if meta.OwnerID == owner {
			out = append(out, meta)
		}
	}
	return out, nil
}

func (s *recordingSessionStore) UpdateConfig(ctx context.Context, actor session.Actor, id string, cfg session.SessionConfig) error {
	return nil
}

func (s *recordingSessionStore) Touch(ctx context.Context, actor session.Actor, id string) error {
	return nil
}

func TestSessionServiceUsesInjectedSessionStore(t *testing.T) {
	ctx := context.Background()
	store := &recordingSessionStore{}
	svc, err := NewSessionService(SessionConfig{
		ProviderConfigPath: writeProvidersConfig(t),
		ProviderName:       "fake-local",
		Store:              store,
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	actor := session.Actor{UserID: "alice"}
	created, err := svc.Create(ctx, actor, "alice prompt")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.OwnerID != "alice" {
		t.Fatalf("Create() OwnerID = %q, want alice", created.OwnerID)
	}
	if store.actor.UserID != "alice" {
		t.Fatalf("store actor = %q, want alice", store.actor.UserID)
	}
	listed, err := svc.List(ctx, actor)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("List() = %#v, want injected session", listed)
	}
}
```

If `writeProvidersConfig` is not available with that name in the file, use the existing helper already used by other `NewSessionService` tests. Do not create a second config helper if one exists.

- [ ] **Step 2: Run test to verify RED**

Run:

```bash
go test ./internal/application/service -run TestSessionServiceUsesInjectedSessionStore -count=1
```

Expected: FAIL to compile with `unknown field Store in struct literal of type SessionConfig`.

- [ ] **Step 3: Update service to use the interface**

In `internal/application/service/session.go`, update `SessionConfig`:

```go
// Store 覆盖 session metadata store；为空时使用 SessionRoot 创建 file-backed store。
Store session.SessionStore
```

Update `SessionService` fields:

```go
// SessionService 编排 session metadata、transcript 和 Agent run。
type SessionService struct {
	store      session.SessionStore
	config     session.Config
	basePrompt string

	// sessionRunLocks 按 session ID 串行化会推进 transcript leaf 的 Run/Stream。
	sessionRunLocksMu sync.Mutex
	sessionRunLocks   map[string]chan struct{}
}
```

Update `NewSessionService` setup:

```go
store := cfg.Store
if store == nil {
	store = session.NewManager(cfg.SessionRoot)
}
return &SessionService{
	store: store,
	config: session.Config{
		ProviderConfigPath: cfg.ProviderConfigPath,
		ProviderName:       cfg.ProviderName,
		BaseSystemPrompt:   strings.TrimSpace(cfg.BaseSystemPrompt),
		Logger:             cfg.Logger,
		MaxSteps:           cfg.MaxSteps,
	},
	basePrompt:      strings.TrimSpace(cfg.BaseSystemPrompt),
	sessionRunLocks: make(map[string]chan struct{}),
}, nil
```

Replace every `s.manager.` call in `session.go` with `s.store.`:

```go
return s.store.Create(ctx, actor, title, cfg)
return s.store.List(ctx, actor)
meta, err := s.store.Resolve(ctx, actor, sessionID)
return s.store.UpdateConfig(ctx, actor, meta.ID, cfg)
return s.store.Touch(ctx, actor, meta.ID)
```

- [ ] **Step 4: Run service tests**

Run:

```bash
go test ./internal/application/service -run 'TestSessionServiceUsesInjectedSessionStore|TestSessionServiceRejectsDifferentOwner' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run affected packages**

Run:

```bash
go test ./internal/session ./internal/application/service ./cmd/cli ./internal/application/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/application/service/session.go internal/application/service/session_test.go
git commit -m "refactor: inject session store into service"
```

---

### Task 3: Final verification and scope review

**Files:**
- Modify only if verification reveals a real issue.

- [ ] **Step 1: Run focused store-boundary tests**

Run:

```bash
go test ./internal/session -run TestSessionStoreInterfaceUsesManagerOwnershipRules -count=1
go test ./internal/application/service -run TestSessionServiceUsesInjectedSessionStore -count=1
```

Expected: both commands PASS.

- [ ] **Step 2: Run affected packages**

Run:

```bash
go test ./internal/session ./internal/application/service ./cmd/cli ./internal/application/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run full verification**

Run:

```bash
go test -count=1 ./...
go vet ./...
go test -race ./...
```

Expected: all PASS.

- [ ] **Step 4: Inspect scope**

Run:

```bash
git diff --stat main..HEAD
git diff --name-only main..HEAD
```

Expected: this PR adds store boundary changes only on top of PR 1 and design/plan docs. No GORM, PostgreSQL, SQL migrations, auth/JWT, provider secret storage, or transcript schema implementation appears.

---

## Self Review

Spec coverage:

- `SessionStore` boundary: Task 1.
- File-backed manager remains current implementation: Task 1.
- Service stops depending on concrete manager: Task 2.
- Owner filtering remains at store boundary: Task 1 tests use interface with Alice/Bob isolation.
- No PostgreSQL/GORM/transcript/auth implementation: explicit out-of-scope and final scope check.

Placeholder scan:

- No placeholder markers or unspecified implementation steps.
- Each code-changing task includes concrete code or exact replacement patterns.

Type consistency:

- Interface methods match existing `Manager` method signatures.
- `SessionConfig.Store` type matches `session.SessionStore`.
- Service method signatures remain unchanged from PR 1, so CLI/HTTP callers do not need another API change.
