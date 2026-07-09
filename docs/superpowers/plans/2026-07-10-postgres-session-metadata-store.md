# PostgreSQL Session Metadata Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 PostgreSQL/GORM-backed `session.SessionStore` 实现和 SQL migrations，为后续 runtime 配置切换做准备，但本 PR 不改变 CLI/HTTP 默认 file-backed 行为。

**Architecture:** 新包 `internal/storage/postgres` 持有 `*gorm.DB`、定义 DB record、mapper 和 `SessionStore` 实现。生产 schema 由 `migrations/*.sql` 管理；GORM `AutoMigrate` 仅在单元测试中使用。Transcript 仍是 JSONL 文件，PostgreSQL store 通过 `transcriptRoot + sessionID + .jsonl` 计算 `SessionMeta.Path`，不迁移 transcript 内容。

**Tech Stack:** Go、GORM、PostgreSQL GORM driver、SQLite GORM driver for unit tests、SQL migration files、现有 `session.SessionStore` 接口。

---

## Scope

### In scope

- 新增 SQL migrations: users + sessions metadata schema。
- 新增 `internal/storage/postgres` 包。
- 新增 GORM DB records 和 mapper。
- 新增 `Store` 实现 `session.SessionStore`。
- 用 SQLite in-memory GORM DB 做 store contract 单元测试，验证 owner filtering、not-found 语义、config normalization、updated_at/current behavior equivalent where applicable。
- 新增 GORM/PostgreSQL dependencies。

### Out of scope

- 不接入 `SessionService` 默认配置。
- 不新增应用配置项选择 file/postgres store。
- 不迁移 JSONL transcript 到 PostgreSQL。
- 不实现 auth/JWT/user middleware。
- 不保存 Provider API Key 或完整 provider registry。
- 不实现 PostgreSQL advisory lock 或多实例 run lock。

---

## File Structure

- `go.mod`, `go.sum`
  - Add `gorm.io/gorm`, `gorm.io/driver/postgres`, `gorm.io/driver/sqlite`.
- `migrations/0001_users_sessions.up.sql`
  - Create `users`, `sessions`, index, default `local` user.
- `migrations/0001_users_sessions.down.sql`
  - Drop index and tables in dependency order.
- `internal/storage/postgres/models.go`
  - Define `UserRecord`, `SessionRecord`.
- `internal/storage/postgres/store.go`
  - Define `Store`, constructor, interface assertion, CRUD methods.
- `internal/storage/postgres/mapper.go`
  - Convert between `SessionRecord` and `session.SessionMeta`.
- `internal/storage/postgres/store_test.go`
  - Test store behavior through `session.SessionStore` interface with SQLite in-memory DB.
- `internal/storage/postgres/migrations_test.go`
  - Test migration files contain required schema constraints and default local user.

---

### Task 1: Add dependencies and SQL migrations

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `migrations/0001_users_sessions.up.sql`
- Create: `migrations/0001_users_sessions.down.sql`
- Create: `internal/storage/postgres/migrations_test.go`

- [ ] **Step 1: Add failing migration test**

Create `internal/storage/postgres/migrations_test.go`:

```go
package postgres

import (
	"os"
	"strings"
	"testing"
)

func TestInitialMigrationDefinesUsersSessionsAndLocalUser(t *testing.T) {
	up, err := os.ReadFile("../../../migrations/0001_users_sessions.up.sql")
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	body := strings.ToLower(string(up))
	checks := []string{
		"create table users",
		"id text primary key",
		"email text unique",
		"create table sessions",
		"owner_id text not null references users(id)",
		"provider_name text",
		"session_prompt text",
		"max_steps integer",
		"create index sessions_owner_updated_idx",
		"on sessions(owner_id, updated_at desc)",
		"insert into users",
		"local",
		"local user",
	}
	for _, want := range checks {
		if !strings.Contains(body, want) {
			t.Fatalf("up migration missing %q in:\n%s", want, body)
		}
	}
}

func TestInitialMigrationDownDropsSessionsBeforeUsers(t *testing.T) {
	down, err := os.ReadFile("../../../migrations/0001_users_sessions.down.sql")
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	body := strings.ToLower(string(down))
	sessions := strings.Index(body, "drop table if exists sessions")
	users := strings.Index(body, "drop table if exists users")
	if sessions < 0 || users < 0 || sessions > users {
		t.Fatalf("down migration must drop sessions before users:\n%s", body)
	}
}
```

- [ ] **Step 2: Run test to verify RED**

Run:

```bash
go test ./internal/storage/postgres -run TestInitialMigration -count=1
```

Expected: FAIL because package or migration files do not exist yet.

- [ ] **Step 3: Add GORM dependencies**

Run:

```bash
go get gorm.io/gorm gorm.io/driver/postgres gorm.io/driver/sqlite
```

Expected: `go.mod` and `go.sum` updated.

- [ ] **Step 4: Add migration files**

Create `migrations/0001_users_sessions.up.sql`:

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

insert into users (id, email, display_name, created_at, updated_at)
values ('local', null, 'Local User', now(), now());
```

Create `migrations/0001_users_sessions.down.sql`:

```sql
drop index if exists sessions_owner_updated_idx;
drop table if exists sessions;
drop table if exists users;
```

- [ ] **Step 5: Run migration tests**

Run:

```bash
go test ./internal/storage/postgres -run TestInitialMigration -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum migrations/0001_users_sessions.up.sql migrations/0001_users_sessions.down.sql internal/storage/postgres/migrations_test.go
git commit -m "feat: add postgres session migrations"
```

---

### Task 2: Add records and mappers

**Files:**
- Create: `internal/storage/postgres/models.go`
- Create: `internal/storage/postgres/mapper.go`
- Create: `internal/storage/postgres/mapper_test.go`

- [ ] **Step 1: Write failing mapper tests**

Create `internal/storage/postgres/mapper_test.go`:

```go
package postgres

import (
	"path/filepath"
	"testing"
	"time"

	"harukizmoe/pimoe/internal/session"
)

func TestSessionRecordToMetaComputesTranscriptPathAndConfig(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	createdAt := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	record := SessionRecord{
		ID:            "session-1",
		OwnerID:       "alice",
		Title:         "alice prompt",
		ProviderName:  "fake-local",
		SessionPrompt: "be brief",
		MaxSteps:      3,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}

	meta := sessionRecordToMeta(root, record)

	if meta.ID != "session-1" || meta.OwnerID != "alice" || meta.Title != "alice prompt" {
		t.Fatalf("meta identity = %#v", meta)
	}
	if meta.Path != filepath.Join(root, "session-1.jsonl") {
		t.Fatalf("meta Path = %q, want computed transcript path", meta.Path)
	}
	wantCfg := session.SessionConfig{ProviderName: "fake-local", SessionPrompt: "be brief", MaxSteps: 3}
	if meta.Config != wantCfg {
		t.Fatalf("meta Config = %#v, want %#v", meta.Config, wantCfg)
	}
	if !meta.CreatedAt.Equal(createdAt) || !meta.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("meta times = %s/%s, want %s/%s", meta.CreatedAt, meta.UpdatedAt, createdAt, updatedAt)
	}
}

func TestSessionMetaToRecordDropsTranscriptPath(t *testing.T) {
	createdAt := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	meta := session.SessionMeta{
		ID:        "session-1",
		OwnerID:   "alice",
		Path:      "/tmp/sessions/session-1.jsonl",
		Title:     "alice prompt",
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Config:    session.SessionConfig{ProviderName: "fake-local", SessionPrompt: "be brief", MaxSteps: 3},
	}

	record := sessionMetaToRecord(meta)

	if record.ID != meta.ID || record.OwnerID != meta.OwnerID || record.Title != meta.Title {
		t.Fatalf("record identity = %#v", record)
	}
	if record.ProviderName != "fake-local" || record.SessionPrompt != "be brief" || record.MaxSteps != 3 {
		t.Fatalf("record config fields = %#v", record)
	}
}
```

- [ ] **Step 2: Run test to verify RED**

Run:

```bash
go test ./internal/storage/postgres -run TestSession.*Record -count=1
```

Expected: FAIL to compile with undefined `SessionRecord`, `sessionRecordToMeta`, or `sessionMetaToRecord`.

- [ ] **Step 3: Add GORM record models**

Create `internal/storage/postgres/models.go`:

```go
package postgres

import "time"

// UserRecord 是 PostgreSQL users 表的 GORM record；不要作为业务层用户模型暴露。
type UserRecord struct {
	ID          string  `gorm:"primaryKey;type:text"`
	Email       *string `gorm:"uniqueIndex;type:text"`
	DisplayName string  `gorm:"not null;type:text"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TableName 固定 users 表名，避免 GORM 根据结构体名推断出错。
func (UserRecord) TableName() string { return "users" }

// SessionRecord 是 PostgreSQL sessions 表的 GORM record；transcript 仍保存在 JSONL 文件中。
type SessionRecord struct {
	ID            string     `gorm:"primaryKey;type:text"`
	OwnerID       string     `gorm:"not null;type:text;index:idx_sessions_owner_updated,priority:1"`
	Title         string     `gorm:"not null;type:text"`
	ProviderName  string     `gorm:"type:text"`
	SessionPrompt string     `gorm:"type:text"`
	MaxSteps      int        `gorm:"type:integer"`
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null;index:idx_sessions_owner_updated,priority:2,sort:desc"`
	ArchivedAt    *time.Time `gorm:"index"`
}

// TableName 固定 sessions 表名，和 SQL migration 保持一致。
func (SessionRecord) TableName() string { return "sessions" }
```

- [ ] **Step 4: Add mapper functions**

Create `internal/storage/postgres/mapper.go`:

```go
package postgres

import (
	"path/filepath"

	"harukizmoe/pimoe/internal/session"
)

func sessionRecordToMeta(transcriptRoot string, record SessionRecord) session.SessionMeta {
	return session.SessionMeta{
		ID:        record.ID,
		OwnerID:   record.OwnerID,
		Path:      filepath.Join(transcriptRoot, record.ID+".jsonl"),
		Title:     record.Title,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
		Config: session.SessionConfig{
			ProviderName:   record.ProviderName,
			SessionPrompt: record.SessionPrompt,
			MaxSteps:      record.MaxSteps,
		},
	}
}

func sessionMetaToRecord(meta session.SessionMeta) SessionRecord {
	return SessionRecord{
		ID:            meta.ID,
		OwnerID:       meta.OwnerID,
		Title:         meta.Title,
		ProviderName:  meta.Config.ProviderName,
		SessionPrompt: meta.Config.SessionPrompt,
		MaxSteps:      meta.Config.MaxSteps,
		CreatedAt:     meta.CreatedAt,
		UpdatedAt:     meta.UpdatedAt,
	}
}
```

- [ ] **Step 5: Run mapper tests**

Run:

```bash
go test ./internal/storage/postgres -run TestSession.*Record -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/postgres/models.go internal/storage/postgres/mapper.go internal/storage/postgres/mapper_test.go
git commit -m "feat: add postgres session records"
```

---

### Task 3: Implement PostgreSQL `SessionStore`

**Files:**
- Create: `internal/storage/postgres/store.go`
- Create: `internal/storage/postgres/store_test.go`

- [ ] **Step 1: Write failing store contract tests**

Create `internal/storage/postgres/store_test.go`:

```go
package postgres

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"harukizmoe/pimoe/internal/session"
)

func newTestStore(t *testing.T) (session.SessionStore, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&UserRecord{}, &SessionRecord{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&UserRecord{ID: "local", DisplayName: "Local User", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed local user: %v", err)
	}
	root := filepath.Join(t.TempDir(), "sessions")
	return NewSessionStore(db, root), root
}

func TestSessionStoreCreateResolveListFiltersByOwner(t *testing.T) {
	ctx := context.Background()
	store, root := newTestStore(t)
	alice := session.Actor{UserID: "alice"}
	bob := session.Actor{UserID: "bob"}

	aliceSession, err := store.Create(ctx, alice, "alice prompt", session.SessionConfig{ProviderName: "fake", SessionPrompt: "brief", MaxSteps: 2})
	if err != nil {
		t.Fatalf("Create() alice error = %v", err)
	}
	bobSession, err := store.Create(ctx, bob, "bob prompt", session.SessionConfig{})
	if err != nil {
		t.Fatalf("Create() bob error = %v", err)
	}
	if aliceSession.OwnerID != "alice" || aliceSession.Path != filepath.Join(root, aliceSession.ID+".jsonl") {
		t.Fatalf("alice session = %#v", aliceSession)
	}
	if bobSession.OwnerID != "bob" {
		t.Fatalf("bob OwnerID = %q, want bob", bobSession.OwnerID)
	}

	resolved, err := store.Resolve(ctx, alice, aliceSession.ID)
	if err != nil {
		t.Fatalf("Resolve() alice error = %v", err)
	}
	if resolved.ID != aliceSession.ID || resolved.Config.ProviderName != "fake" || resolved.Config.SessionPrompt != "brief" || resolved.Config.MaxSteps != 2 {
		t.Fatalf("Resolve() = %#v, want alice config", resolved)
	}
	if _, err := store.Resolve(ctx, bob, aliceSession.ID); !session.IsNotFound(err) {
		t.Fatalf("Resolve() bob on alice err = %v, want not found", err)
	}
	aliceList, err := store.List(ctx, alice)
	if err != nil {
		t.Fatalf("List() alice error = %v", err)
	}
	if len(aliceList) != 1 || aliceList[0].ID != aliceSession.ID {
		t.Fatalf("List() alice = %#v, want only alice session", aliceList)
	}
}

func TestSessionStoreTouchAndUpdateConfigRequireOwner(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	alice := session.Actor{UserID: "alice"}
	bob := session.Actor{UserID: "bob"}

	created, err := store.Create(ctx, alice, "alice prompt", session.SessionConfig{ProviderName: "old"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Touch(ctx, bob, created.ID); !session.IsNotFound(err) {
		t.Fatalf("Touch() bob error = %v, want not found", err)
	}
	updatedCfg := session.SessionConfig{ProviderName: "new", SessionPrompt: "keep", MaxSteps: 4}
	if err := store.UpdateConfig(ctx, alice, created.ID, updatedCfg); err != nil {
		t.Fatalf("UpdateConfig() alice error = %v", err)
	}
	resolved, err := store.Resolve(ctx, alice, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Config != updatedCfg {
		t.Fatalf("Config = %#v, want %#v", resolved.Config, updatedCfg)
	}
	if !resolved.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("UpdatedAt = %s, want after %s", resolved.UpdatedAt, created.UpdatedAt)
	}
	if err := store.UpdateConfig(ctx, bob, created.ID, session.SessionConfig{}); !session.IsNotFound(err) {
		t.Fatalf("UpdateConfig() bob error = %v, want not found", err)
	}
}

func TestSessionStoreNormalizesBlankActorToLocal(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t)
	created, err := store.Create(ctx, session.Actor{}, "local prompt", session.SessionConfig{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.OwnerID != "local" {
		t.Fatalf("OwnerID = %q, want local", created.OwnerID)
	}
	if _, err := store.Resolve(ctx, session.LocalActor(), created.ID); err != nil {
		t.Fatalf("Resolve() local error = %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/storage/postgres -run 'TestSessionStore' -count=1
```

Expected: FAIL to compile with undefined `NewSessionStore` or `Store`.

- [ ] **Step 3: Implement store**

Create `internal/storage/postgres/store.go`:

```go
package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"harukizmoe/pimoe/internal/session"
)

const defaultTranscriptRoot = ".moe/sessions"

// Store 是 PostgreSQL/GORM-backed session metadata store；transcript 仍保存在 JSONL 文件中。
type Store struct {
	db             *gorm.DB
	transcriptRoot string
}

var _ session.SessionStore = (*Store)(nil)

// NewSessionStore 创建 PostgreSQL session metadata store；db 由调用方负责生命周期管理。
func NewSessionStore(db *gorm.DB, transcriptRoot string) *Store {
	transcriptRoot = strings.TrimSpace(transcriptRoot)
	if transcriptRoot == "" {
		transcriptRoot = defaultTranscriptRoot
	}
	return &Store{db: db, transcriptRoot: transcriptRoot}
}

// Create 创建一条归属 actor 的 session metadata；缺失用户会按最小本地语义创建用户记录。
func (s *Store) Create(ctx context.Context, actor session.Actor, title string, cfg session.SessionConfig) (session.SessionMeta, error) {
	if err := s.ready(ctx); err != nil {
		return session.SessionMeta{}, err
	}
	actor = session.NormalizeActor(actor)
	now := time.Now().UTC()
	id, err := newSessionID(now)
	if err != nil {
		return session.SessionMeta{}, err
	}
	record := SessionRecord{
		ID:            id,
		OwnerID:       actor.UserID,
		Title:         normalizeTitle(title),
		ProviderName:  strings.TrimSpace(cfg.ProviderName),
		SessionPrompt: strings.TrimSpace(cfg.SessionPrompt),
		MaxSteps:      normalizeMaxSteps(cfg.MaxSteps),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ensureUser(ctx, tx, actor.UserID, now); err != nil {
			return err
		}
		return tx.Create(&record).Error
	})
	if err != nil {
		return session.SessionMeta{}, fmt.Errorf("create session metadata: %w", err)
	}
	return sessionRecordToMeta(s.transcriptRoot, record), nil
}

// Resolve 返回 actor 拥有的 session metadata；owner 不匹配时返回 not found 风格错误。
func (s *Store) Resolve(ctx context.Context, actor session.Actor, id string) (session.SessionMeta, error) {
	if err := s.ready(ctx); err != nil {
		return session.SessionMeta{}, err
	}
	actor = session.NormalizeActor(actor)
	var record SessionRecord
	err := s.db.WithContext(ctx).Where("id = ? and owner_id = ?", id, actor.UserID).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return session.SessionMeta{}, sessionNotFound(id)
	}
	if err != nil {
		return session.SessionMeta{}, fmt.Errorf("resolve session metadata: %w", err)
	}
	return sessionRecordToMeta(s.transcriptRoot, record), nil
}

// List 按 updated_at 倒序返回 actor 拥有的 sessions。
func (s *Store) List(ctx context.Context, actor session.Actor) ([]session.SessionMeta, error) {
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	actor = session.NormalizeActor(actor)
	var records []SessionRecord
	if err := s.db.WithContext(ctx).Where("owner_id = ?", actor.UserID).Order("updated_at desc").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list session metadata: %w", err)
	}
	out := make([]session.SessionMeta, 0, len(records))
	for _, record := range records {
		out = append(out, sessionRecordToMeta(s.transcriptRoot, record))
	}
	return out, nil
}

// UpdateConfig 更新 actor 拥有的 session 偏好和 updated_at。
func (s *Store) UpdateConfig(ctx context.Context, actor session.Actor, id string, cfg session.SessionConfig) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	actor = session.NormalizeActor(actor)
	updates := map[string]any{
		"provider_name":  strings.TrimSpace(cfg.ProviderName),
		"session_prompt": strings.TrimSpace(cfg.SessionPrompt),
		"max_steps":      normalizeMaxSteps(cfg.MaxSteps),
		"updated_at":     time.Now().UTC(),
	}
	result := s.db.WithContext(ctx).Model(&SessionRecord{}).Where("id = ? and owner_id = ?", id, actor.UserID).Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("update session metadata: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return sessionNotFound(id)
	}
	return nil
}

// Touch 更新 actor 拥有的 session updated_at。
func (s *Store) Touch(ctx context.Context, actor session.Actor, id string) error {
	if err := s.ready(ctx); err != nil {
		return err
	}
	actor = session.NormalizeActor(actor)
	result := s.db.WithContext(ctx).Model(&SessionRecord{}).Where("id = ? and owner_id = ?", id, actor.UserID).Update("updated_at", time.Now().UTC())
	if result.Error != nil {
		return fmt.Errorf("touch session metadata: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return sessionNotFound(id)
	}
	return nil
}

func (s *Store) ready(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres session store is nil")
	}
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func ensureUser(ctx context.Context, tx *gorm.DB, userID string, now time.Time) error {
	user := UserRecord{ID: userID, DisplayName: userID, CreatedAt: now, UpdatedAt: now}
	return tx.WithContext(ctx).FirstOrCreate(&user, UserRecord{ID: userID}).Error
}

func normalizeTitle(title string) string {
	line := strings.TrimSpace(strings.Split(strings.ReplaceAll(title, "\r\n", "\n"), "\n")[0])
	if line == "" {
		return "untitled session"
	}
	if len(line) > 80 {
		return line[:80]
	}
	return line
}

func normalizeMaxSteps(maxSteps int) int {
	if maxSteps < 1 {
		return 0
	}
	return maxSteps
}

func newSessionID(now time.Time) (string, error) {
	var random [3]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(random[:]), nil
}

type notFoundError struct{ id string }

func (e notFoundError) Error() string { return fmt.Sprintf("session %q not found", e.id) }

func sessionNotFound(id string) error { return notFoundError{id: id} }
```

- [ ] **Step 4: Update session not-found compatibility if needed**

If `session.IsNotFound(err)` does not recognize the postgres package-local `notFoundError`, change `internal/session/store.go` to expose a constructor instead of duplicating the type:

```go
// NewNotFoundError returns the canonical not-found error for session metadata stores.
func NewNotFoundError(id string) error {
	return notFoundError{id: id}
}
```

Then replace `sessionNotFound(id)` in postgres store with `session.NewNotFoundError(id)` and remove package-local `notFoundError` / `sessionNotFound`. Add or update comments in Chinese for the exported constructor.

- [ ] **Step 5: Run store tests**

Run:

```bash
go test ./internal/storage/postgres -run 'TestSessionStore' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run package tests**

Run:

```bash
go test ./internal/session ./internal/storage/postgres -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/session/store.go internal/storage/postgres/store.go internal/storage/postgres/store_test.go
git commit -m "feat: add postgres session store"
```

---

### Task 4: Final verification and scope review

**Files:**
- Modify only if verification reveals a real issue.

- [ ] **Step 1: Run focused PostgreSQL store tests**

Run:

```bash
go test ./internal/storage/postgres -count=1
```

Expected: PASS.

- [ ] **Step 2: Run affected packages**

Run:

```bash
go test ./internal/session ./internal/storage/postgres ./internal/application/service ./cmd/cli ./internal/application/... -count=1
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

Expected: changes add PR 3 PostgreSQL metadata store on top of previous ownership/store-boundary work. No runtime default switch, auth/JWT, provider secret storage, transcript database schema, or advisory lock implementation appears.

---

## Self Review

Spec coverage:

- SQL migrations: Task 1.
- GORM records separate from business models: Task 2.
- Mapper functions: Task 2.
- `SessionStore` implementation: Task 3.
- Owner filtering in store queries: Task 3 uses `where id = ? and owner_id = ?` and `where owner_id = ?`.
- Transcript not migrated: mapper computes JSONL path from transcript root; no event table.
- No runtime switch: no service/CLI/HTTP default configuration changes.

Placeholder scan:

- No placeholder markers or unspecified implementation steps.
- Each code-changing task includes concrete code or exact replacement patterns.

Type consistency:

- `Store` implements current `session.SessionStore` interface.
- `SessionRecord` fields match migration column names.
- Tests use `session.SessionStore` interface to avoid depending on concrete store methods.
