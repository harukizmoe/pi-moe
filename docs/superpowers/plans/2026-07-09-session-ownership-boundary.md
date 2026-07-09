# Session Ownership Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add explicit `Actor` / `OwnerID` session ownership on the current file-backed session manager while preserving local single-user CLI/HTTP behavior.

**Architecture:** Keep the current file-backed `session.Manager` and JSONL transcript. Add a small `session.Actor` model, persist `owner_id` in `index.json`, filter/resolve metadata by actor, and update `SessionService` plus CLI/HTTP callers to pass `session.LocalActor()`. No database, GORM, auth, JWT, or transcript migration in this PR.

**Tech Stack:** Go, current file-backed session manager, existing fake provider tests, standard library only.

---

## File Structure

- `internal/session/manager.go`
  - Add `Actor`, `LocalActor`, `NormalizeActor`, and `SessionMeta.OwnerID`.
  - Persist `owner_id` on `sessionMetaEntry`.
  - Change `Create`, `Resolve`, `List`, `Touch`, and `UpdateConfig` to accept `Actor` and enforce owner filtering.
  - Treat legacy empty `owner_id` as `local` when reading old index records.

- `internal/session/manager_test.go`
  - Add owner persistence/filtering tests.
  - Add unauthorized access tests that expect not-found semantics.
  - Add legacy empty owner compatibility test.
  - Update existing manager tests to pass `LocalActor()`.

- `internal/application/service/session.go`
  - Change service methods to accept `session.Actor`: `Create`, `List`, `Get`, `Run`, `Stream`.
  - Pass actor through manager calls.
  - Preserve provider config behavior and per-session run lock behavior.

- `internal/application/service/session_test.go`
  - Update existing service tests to pass `session.LocalActor()`.
  - Add service-level owner isolation test.

- `internal/application/router/*.go` and router tests
  - Pass `session.LocalActor()` from HTTP handlers for now.
  - Keep response shape unchanged except metadata may now include `owner_id` if current JSON response mirrors `SessionMeta` directly.

- `cmd/cli/main.go` and `cmd/cli/main_test.go`
  - Pass `session.LocalActor()` for managed session create/list/get/run flows.
  - Keep CLI UX unchanged.

---

### Task 1: Add Actor and owner enforcement to session.Manager

**Files:**
- Modify: `internal/session/manager.go`
- Modify: `internal/session/manager_test.go`

- [ ] **Step 1: Write failing manager ownership tests**

Append these tests to `internal/session/manager_test.go`:

```go
func TestManagerCreateResolveListFiltersByOwner(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))
	alice := Actor{UserID: "alice"}
	bob := Actor{UserID: "bob"}

	aliceSession, err := manager.Create(context.Background(), alice, "alice prompt", SessionConfig{ProviderName: "fake"})
	if err != nil {
		t.Fatalf("Create() alice error = %v", err)
	}
	bobSession, err := manager.Create(context.Background(), bob, "bob prompt", SessionConfig{})
	if err != nil {
		t.Fatalf("Create() bob error = %v", err)
	}
	if aliceSession.OwnerID != "alice" {
		t.Fatalf("alice OwnerID = %q, want alice", aliceSession.OwnerID)
	}
	if bobSession.OwnerID != "bob" {
		t.Fatalf("bob OwnerID = %q, want bob", bobSession.OwnerID)
	}

	resolved, err := manager.Resolve(context.Background(), alice, aliceSession.ID)
	if err != nil {
		t.Fatalf("Resolve() alice error = %v", err)
	}
	if resolved.ID != aliceSession.ID || resolved.OwnerID != "alice" {
		t.Fatalf("Resolve() = %#v, want alice session", resolved)
	}
	if _, err := manager.Resolve(context.Background(), bob, aliceSession.ID); !IsNotFound(err) {
		t.Fatalf("Resolve() bob on alice err = %v, want not found", err)
	}

	aliceList, err := manager.List(context.Background(), alice)
	if err != nil {
		t.Fatalf("List() alice error = %v", err)
	}
	if len(aliceList) != 1 || aliceList[0].ID != aliceSession.ID {
		t.Fatalf("List() alice = %#v, want only alice session", aliceList)
	}
	bobList, err := manager.List(context.Background(), bob)
	if err != nil {
		t.Fatalf("List() bob error = %v", err)
	}
	if len(bobList) != 1 || bobList[0].ID != bobSession.ID {
		t.Fatalf("List() bob = %#v, want only bob session", bobList)
	}
}

func TestManagerOwnerMismatchCannotTouchOrUpdateConfig(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "sessions"))
	alice := Actor{UserID: "alice"}
	bob := Actor{UserID: "bob"}
	created, err := manager.Create(context.Background(), alice, "prompt", SessionConfig{ProviderName: "old"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := manager.Touch(context.Background(), bob, created.ID); !IsNotFound(err) {
		t.Fatalf("Touch() owner mismatch err = %v, want not found", err)
	}
	if err := manager.UpdateConfig(context.Background(), bob, created.ID, SessionConfig{ProviderName: "new"}); !IsNotFound(err) {
		t.Fatalf("UpdateConfig() owner mismatch err = %v, want not found", err)
	}

	resolved, err := manager.Resolve(context.Background(), alice, created.ID)
	if err != nil {
		t.Fatalf("Resolve() after rejected writes error = %v", err)
	}
	if resolved.Config.ProviderName != "old" {
		t.Fatalf("ProviderName = %q, want old", resolved.Config.ProviderName)
	}
}

func TestManagerReadsLegacyEmptyOwnerAsLocal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	indexJSON := `{
  "current": "sess-legacy",
  "sessions": [
    {
      "id": "sess-legacy",
      "path": "` + filepath.ToSlash(filepath.Join(root, "sess-legacy.jsonl")) + `",
      "title": "legacy prompt",
      "created_at": "2026-07-09T00:00:00Z",
      "updated_at": "2026-07-09T00:00:00Z"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(indexJSON), 0o600); err != nil {
		t.Fatalf("WriteFile(index) error = %v", err)
	}
	manager := NewManager(root)

	resolved, err := manager.Resolve(context.Background(), LocalActor(), "sess-legacy")
	if err != nil {
		t.Fatalf("Resolve() legacy local error = %v", err)
	}
	if resolved.OwnerID != LocalActor().UserID {
		t.Fatalf("legacy OwnerID = %q, want local", resolved.OwnerID)
	}
	if _, err := manager.Resolve(context.Background(), Actor{UserID: "alice"}, "sess-legacy"); !IsNotFound(err) {
		t.Fatalf("Resolve() non-local legacy err = %v, want not found", err)
	}
}
```

- [ ] **Step 2: Run manager tests to verify RED**

Run:

```bash
go test ./internal/session -run 'TestManager(CreateResolveListFiltersByOwner|OwnerMismatchCannotTouchOrUpdateConfig|ReadsLegacyEmptyOwnerAsLocal)' -count=1
```

Expected: build fails because `Actor`, `LocalActor`, and actor-aware manager method signatures do not exist.

- [ ] **Step 3: Implement actor model and manager ownership**

In `internal/session/manager.go`, add after the constants:

```go
const localActorUserID = "local"

// Actor 表示当前请求的业务身份；认证方式由上层入口负责。
type Actor struct {
	// UserID 是 session owner 边界使用的稳定用户标识；空值会归一化为 local。
	UserID string
}

// LocalActor 返回当前 CLI 和未认证 HTTP 使用的默认单用户身份。
func LocalActor() Actor {
	return Actor{UserID: localActorUserID}
}

// NormalizeActor 将空白用户归一化为 local，避免调用方漏传导致无 owner session。
func NormalizeActor(actor Actor) Actor {
	actor.UserID = strings.TrimSpace(actor.UserID)
	if actor.UserID == "" {
		actor.UserID = localActorUserID
	}
	return actor
}
```

Update structs:

```go
type SessionMeta struct {
	ID        string
	OwnerID   string
	Path      string
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
	Config    SessionConfig
}

type sessionMetaEntry struct {
	ID        string        `json:"id"`
	OwnerID   string        `json:"owner_id,omitempty"`
	Path      string        `json:"path"`
	Title     string        `json:"title"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Config    SessionConfig `json:"config,omitempty"`
}
```

Change manager methods to these signatures and behaviors:

```go
func (m *Manager) Create(ctx context.Context, actor Actor, title string, cfg SessionConfig) (SessionMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SessionMeta{}, err
	}
	actor = NormalizeActor(actor)
	m.mu.Lock()
	defer m.mu.Unlock()
	index, err := m.loadIndex()
	if err != nil {
		return SessionMeta{}, err
	}
	now := nowUTC()
	id, err := newSessionID(now)
	if err != nil {
		return SessionMeta{}, err
	}
	meta := sessionMetaEntry{
		ID:        id,
		OwnerID:   actor.UserID,
		Path:      filepath.Join(m.root, id+".jsonl"),
		Title:     normalizeSessionTitle(title),
		CreatedAt: now,
		UpdatedAt: now,
		Config:    normalizeSessionConfig(cfg),
	}
	index.Current = id
	index.Sessions = append(index.Sessions, meta)
	if err := m.saveIndex(index); err != nil {
		return SessionMeta{}, err
	}
	return meta.toSessionMeta(), nil
}

func (m *Manager) Resolve(ctx context.Context, actor Actor, id string) (SessionMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SessionMeta{}, err
	}
	actor = NormalizeActor(actor)
	index, err := m.loadIndex()
	if err != nil {
		return SessionMeta{}, err
	}
	for _, meta := range index.Sessions {
		if meta.ID == id && meta.ownerID() == actor.UserID {
			return meta.toSessionMeta(), nil
		}
	}
	return SessionMeta{}, notFoundError{id: id}
}

func (m *Manager) List(ctx context.Context, actor Actor) ([]SessionMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	actor = NormalizeActor(actor)
	index, err := m.loadIndex()
	if err != nil {
		return nil, err
	}
	metas := make([]SessionMeta, 0, len(index.Sessions))
	for _, entry := range index.Sessions {
		if entry.ownerID() == actor.UserID {
			metas = append(metas, entry.toSessionMeta())
		}
	}
	sort.SliceStable(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})
	return metas, nil
}

func (m *Manager) Touch(ctx context.Context, actor Actor, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actor = NormalizeActor(actor)
	m.mu.Lock()
	defer m.mu.Unlock()
	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	for i := range index.Sessions {
		if index.Sessions[i].ID == id && index.Sessions[i].ownerID() == actor.UserID {
			index.Sessions[i].OwnerID = actor.UserID
			index.Sessions[i].UpdatedAt = nowUTC()
			index.Current = id
			return m.saveIndex(index)
		}
	}
	return notFoundError{id: id}
}

func (m *Manager) UpdateConfig(ctx context.Context, actor Actor, id string, cfg SessionConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actor = NormalizeActor(actor)
	m.mu.Lock()
	defer m.mu.Unlock()
	index, err := m.loadIndex()
	if err != nil {
		return err
	}
	for i := range index.Sessions {
		if index.Sessions[i].ID == id && index.Sessions[i].ownerID() == actor.UserID {
			index.Sessions[i].OwnerID = actor.UserID
			index.Sessions[i].Config = normalizeSessionConfig(cfg)
			index.Sessions[i].UpdatedAt = nowUTC()
			index.Current = id
			return m.saveIndex(index)
		}
	}
	return notFoundError{id: id}
}
```

Add helper near `toSessionMeta`:

```go
func (e sessionMetaEntry) ownerID() string {
	return NormalizeActor(Actor{UserID: e.OwnerID}).UserID
}
```

Update `toSessionMeta` to set `OwnerID: e.ownerID()`.

- [ ] **Step 4: Update existing manager tests to pass LocalActor**

In `internal/session/manager_test.go`, replace existing calls:

```go
manager.Create(context.Background(), "prompt", cfg)
manager.Resolve(context.Background(), id)
manager.List(context.Background())
manager.Touch(context.Background(), id)
manager.UpdateConfig(context.Background(), id, cfg)
```

with:

```go
manager.Create(context.Background(), LocalActor(), "prompt", cfg)
manager.Resolve(context.Background(), LocalActor(), id)
manager.List(context.Background(), LocalActor())
manager.Touch(context.Background(), LocalActor(), id)
manager.UpdateConfig(context.Background(), LocalActor(), id, cfg)
```

Apply the same replacement to all manager tests, including concurrent create goroutines.

- [ ] **Step 5: Run manager tests to verify GREEN**

Run:

```bash
gofmt -w internal/session/manager.go internal/session/manager_test.go
go test ./internal/session -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit manager ownership**

```bash
git add internal/session/manager.go internal/session/manager_test.go
git commit -m "feat: add session ownership metadata"
```

---

### Task 2: Thread Actor through SessionService

**Files:**
- Modify: `internal/application/service/session.go`
- Modify: `internal/application/service/session_test.go`
- Modify: `internal/application/service/session_lock_test.go`

- [ ] **Step 1: Write failing service owner isolation test**

Append to `internal/application/service/session_test.go`:

```go
func TestSessionServiceRejectsDifferentOwner(t *testing.T) {
	ctx := context.Background()
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		SessionRoot:        filepath.Join(t.TempDir(), "sessions"),
		ProviderConfigPath: writeProvidersConfig(t),
		ProviderName:       "fake-local",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	alice := session.Actor{UserID: "alice"}
	bob := session.Actor{UserID: "bob"}

	created, err := svc.Create(ctx, alice, "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.OwnerID != "alice" {
		t.Fatalf("Create() OwnerID = %q, want alice", created.OwnerID)
	}
	if _, err := svc.Get(ctx, bob, created.ID); !session.IsNotFound(err) {
		t.Fatalf("Get() bob err = %v, want not found", err)
	}
	if _, err := svc.Run(ctx, bob, created.ID, "hello"); !session.IsNotFound(err) {
		t.Fatalf("Run() bob err = %v, want not found", err)
	}
	listed, err := svc.List(ctx, bob)
	if err != nil {
		t.Fatalf("List() bob error = %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("List() bob = %#v, want empty", listed)
	}
}
```

- [ ] **Step 2: Run service owner test to verify RED**

Run:

```bash
go test ./internal/application/service -run TestSessionServiceRejectsDifferentOwner -count=1
```

Expected: build fails until service methods accept `session.Actor`.

- [ ] **Step 3: Update SessionService signatures and manager calls**

In `internal/application/service/session.go`, change public method signatures:

```go
func (s *SessionService) Create(ctx context.Context, actor session.Actor, title string, opts ...CreateOptions) (SessionMeta, error)
func (s *SessionService) List(ctx context.Context, actor session.Actor) ([]SessionMeta, error)
func (s *SessionService) Get(ctx context.Context, actor session.Actor, sessionID string) (SessionDetail, error)
func (s *SessionService) Run(ctx context.Context, actor session.Actor, sessionID string, input string, opts ...RunOptions) (RunResult, error)
func (s *SessionService) Stream(ctx context.Context, actor session.Actor, sessionID string, input string, opts ...RunOptions) (<-chan StreamEvent, error)
```

Within these methods, pass actor into manager calls:

```go
return s.manager.Create(ctx, actor, title, cfg)
return s.manager.List(ctx, actor)
meta, err := s.manager.Resolve(ctx, actor, sessionID)
if err != nil {
	return RunResult{}, err
}
unlock, err := s.lockSessionRun(ctx, meta.ID)
if err != nil {
	return RunResult{}, err
}
defer unlock()
meta, err = s.manager.Resolve(ctx, actor, meta.ID)
if err != nil {
	return RunResult{}, err
}
if err := s.persistRunSuccess(ctx, actor, meta, providerName, override); err != nil {
	return result, err
}
```

Change `persistRunSuccess` signature and body:

```go
func (s *SessionService) persistRunSuccess(ctx context.Context, actor session.Actor, meta SessionMeta, providerName string, override bool) error {
	if override {
		cfg := meta.Config
		cfg.ProviderName = providerName
		return s.manager.UpdateConfig(ctx, actor, meta.ID, cfg)
	}
	return s.manager.Touch(ctx, actor, meta.ID)
}
```

Keep lock key as `meta.ID`, not `actor.UserID + meta.ID`, because `meta.ID` is globally unique.

- [ ] **Step 4: Update service tests to use LocalActor**

In `internal/application/service/session_test.go` and `internal/application/service/session_lock_test.go`, update existing calls:

```go
svc.Create(ctx, "prompt")
svc.List(ctx)
svc.Get(ctx, id)
svc.Run(ctx, id, "prompt")
svc.Stream(ctx, id, "prompt")
```

with:

```go
svc.Create(ctx, session.LocalActor(), "prompt")
svc.List(ctx, session.LocalActor())
svc.Get(ctx, session.LocalActor(), id)
svc.Run(ctx, session.LocalActor(), id, "prompt")
svc.Stream(ctx, session.LocalActor(), id, "prompt")
```

For tests that already import `harukizmoe/pimoe/internal/session`, reuse it. If a test file lacks the import, add:

```go
"harukizmoe/pimoe/internal/session"
```

- [ ] **Step 5: Run service tests**

Run:

```bash
gofmt -w internal/application/service/session.go internal/application/service/session_test.go internal/application/service/session_lock_test.go
go test ./internal/application/service -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit service actor threading**

```bash
git add internal/application/service/session.go internal/application/service/session_test.go internal/application/service/session_lock_test.go
git commit -m "feat: enforce session owner in service"
```

---

### Task 3: Update HTTP and CLI callers to pass LocalActor

**Files:**
- Modify: `cmd/cli/main.go`
- Modify: `cmd/cli/main_test.go`
- Modify: `internal/application/router/*.go`
- Modify: `internal/application/router/*_test.go`
- Modify as needed: `internal/application/http_test.go`

- [ ] **Step 1: Run caller packages to show compile failures**

Run:

```bash
go test ./cmd/cli ./internal/application/... -count=1
```

Expected: build fails at call sites still using old `SessionService` signatures.

- [ ] **Step 2: Update CLI call sites**

In `cmd/cli/main.go`, import/use existing `session` package and define one local actor where managed sessions are handled:

```go
actor := session.LocalActor()
```

Update managed session calls:

```go
created, err := sessionService.Create(ctx, actor, prompt, appservice.CreateOptions{ProviderName: opts.providerName, SessionPrompt: opts.sessionPrompt, MaxSteps: opts.maxSteps})
listed, err := sessionService.List(ctx, actor)
detail, err := sessionService.Get(ctx, actor, opts.sessionID)
result, err := sessionService.Run(ctx, actor, sessionID, prompt, appservice.RunOptions{ProviderName: opts.providerName})
stream, err := sessionService.Stream(ctx, actor, sessionID, prompt, appservice.RunOptions{ProviderName: opts.providerName})
```

Do not change raw `--session` JSONL mode unless it uses managed `SessionService`.

- [ ] **Step 3: Update HTTP router call sites**

In router handlers, use `session.LocalActor()` for every `SessionService` call. Example pattern:

```go
actor := session.LocalActor()
created, err := h.service.Create(ctx.Request.Context(), actor, title, appservice.CreateOptions{ProviderName: req.ProviderName, MaxSteps: req.MaxSteps, SessionPrompt: req.SessionPrompt})
listed, err := h.service.List(ctx.Request.Context(), actor)
detail, err := h.service.Get(ctx.Request.Context(), actor, ctx.Param("id"))
result, err := h.service.Run(ctx.Request.Context(), actor, ctx.Param("id"), req.Input, appservice.RunOptions{ProviderName: req.ProviderName})
stream, err := h.service.Stream(ctx.Request.Context(), actor, ctx.Param("id"), req.Input, appservice.RunOptions{ProviderName: req.ProviderName})
```

If the router package does not yet import `internal/session`, add:

```go
"harukizmoe/pimoe/internal/session"
```

- [ ] **Step 4: Update CLI/router tests if JSON expectations include owner_id**

If HTTP JSON responses marshal `SessionMeta` directly and tests compare full JSON, include:

```json
"OwnerID":"local"
```

or, if JSON tags are added later, include:

```json
"owner_id":"local"
```

Prefer not changing external JSON shape in this PR unless current marshaling already exposes Go field names. If response DTOs exist, keep owner internal and only adjust tests for compile errors.

- [ ] **Step 5: Run caller package tests**

Run:

```bash
gofmt -w cmd/cli/main.go cmd/cli/main_test.go internal/application/router internal/application/http_test.go
go test ./cmd/cli ./internal/application/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit caller updates**

```bash
git add cmd/cli/main.go cmd/cli/main_test.go internal/application/router internal/application/http_test.go
git commit -m "chore: use local actor at app entrypoints"
```

---

### Task 4: Final compatibility and regression verification

**Files:**
- Modify only if verification reveals a real compile/test issue.

- [ ] **Step 1: Run focused ownership tests**

Run:

```bash
go test ./internal/session -run 'TestManager(CreateResolveListFiltersByOwner|OwnerMismatchCannotTouchOrUpdateConfig|ReadsLegacyEmptyOwnerAsLocal)' -count=1
go test ./internal/application/service -run TestSessionServiceRejectsDifferentOwner -count=1
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

- [ ] **Step 4: Inspect diff for accidental scope creep**

Run:

```bash
git diff --stat main..HEAD
git diff --name-only main..HEAD
```

Expected: changes are limited to ownership boundary, call sites, tests, and the already committed design/plan docs. No GORM, PostgreSQL, migrations, auth, JWT, or transcript schema implementation appears in this PR.

- [ ] **Step 5: Commit any final fixes**

Only if Step 1-4 required edits:

```bash
git add <changed-files>
git commit -m "test: verify session ownership boundary"
```

If no edits were required, do not create an empty commit.

---

## Self-Review

Spec coverage:

- Actor / LocalActor: Task 1.
- SessionMeta OwnerID and index persistence: Task 1.
- Legacy empty owner as local: Task 1.
- List/Resolve/Get/Run/Stream owner checks: Tasks 1 and 2.
- CLI/HTTP current local actor compatibility: Task 3.
- No GORM/PostgreSQL/auth/transcript migration: explicitly excluded and checked in Task 4.

Placeholder scan:

- No placeholder markers or unspecified implementation steps.
- Each code-changing task includes concrete code or exact replacement patterns.

Type consistency:

- `session.Actor`, `session.LocalActor()`, and `session.NormalizeActor()` are defined in Task 1 before service/router/CLI use them.
- Manager method signatures are updated before `SessionService` calls them.
- `SessionService` method signatures are updated before CLI/router calls them.
