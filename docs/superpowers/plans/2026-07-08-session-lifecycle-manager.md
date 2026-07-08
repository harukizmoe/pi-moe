# Session Lifecycle Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 CLI 增加本地 session 生命周期管理：新建、恢复、列出，并继续保留显式 `--session <path>` 入口。

**Architecture:** `internal/session.Session` 继续只负责 runtime transcript 和 JSONL-backed `Open`；新增 `internal/session.Manager` 只管理 `.moe/sessions/index.json` 和 session 文件路径。CLI 解析 lifecycle flags 后通过 Manager 得到 path，再调用现有 `session.Open`，`--list-sessions` 不加载 Provider、不创建 Session。

**Tech Stack:** Go 标准库、JSON、现有 `internal/session`、现有 fake provider 测试、现有 CLI flag parser。

---

## Preconditions

- 当前 `session-file-resume` 代码改动必须先单独提交，避免 lifecycle 工作与已有未提交 diff 混在一起。
- 已批准设计文档：`docs/superpowers/specs/2026-07-08-session-lifecycle-design.md`。
- 本计划不实现 branch、compaction、memory、HTTP API、database、自动摘要或搜索。

---

## File Structure

- Create: `internal/session/manager.go`
  - Defines `Manager`, `SessionMeta`, JSON index schema, default root constant, Create/Resolve/List/Touch helpers.
  - Does not import `internal/agent`, `internal/llms`, `internal/tools`, or provider config packages.

- Create: `internal/session/manager_test.go`
  - Tests manager-only behavior with temp dirs and deterministic assertions.
  - No provider config, no LLM, no Agent run.

- Modify: `cmd/cli/main.go`
  - Extends `cliOptions` with lifecycle flags.
  - Adds option validation and session resolution helpers.
  - Keeps `--session <path>` behavior unchanged.
  - Makes `--list-sessions` return before logger/provider/session creation if possible.

- Modify: `cmd/cli/main_test.go`
  - Tests parsing, conflict validation, list output, and new/resume transcript reuse.
  - Uses temp dirs and fake provider config.

- Do not modify: `internal/agent`, `internal/llms`, `internal/tools`, `internal/config`, JSONL transcript schema in `internal/session/storage.go`.

---

## Task 0: Clean Baseline

**Files:** none expected, commit existing work only if still uncommitted.

- [ ] **Step 1: Confirm worktree contains no uncommitted session-file-resume code before lifecycle edits**

Run:

```bash
git status --short
```

Expected after baseline cleanup: no `M cmd/cli/main.go`, `M cmd/cli/main_test.go`, `M internal/session/session.go`, `M internal/session/session_test.go`, or `?? internal/session/storage.go` from the previous phase. If they are present, commit or otherwise isolate them before continuing.

- [ ] **Step 2: Run baseline tests**

Run:

```bash
go test ./...
```

Expected: all packages pass. If baseline fails, stop and fix/commit the previous phase first; do not start lifecycle edits on a failing baseline.

---

## Task 1: RED Manager Create/Resolve/List/Touch Contracts

**Files:**
- Create: `internal/session/manager_test.go`
- Later implementation target: `internal/session/manager.go`

- [ ] **Step 1: Write failing manager tests**

Create `internal/session/manager_test.go` with tests shaped like this:

```go
package session

import (
    "context"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"
    "time"
)

func TestManagerCreateResolveListAndTouch(t *testing.T) {
    root := filepath.Join(t.TempDir(), "sessions")
    manager := NewManager(root)

    first, err := manager.Create(context.Background(), "  first prompt\nwith second line  ")
    if err != nil {
        t.Fatalf("Create() first error = %v", err)
    }
    if first.ID == "" {
        t.Fatal("Create() first ID is empty")
    }
    if first.Title != "first prompt" {
        t.Fatalf("Create() first Title = %q, want first prompt", first.Title)
    }
    if filepath.Dir(first.Path) != root {
        t.Fatalf("Create() first Path dir = %q, want %q", filepath.Dir(first.Path), root)
    }
    if filepath.Ext(first.Path) != ".jsonl" {
        t.Fatalf("Create() first Path = %q, want .jsonl extension", first.Path)
    }
    if _, err := os.Stat(filepath.Join(root, "index.json")); err != nil {
        t.Fatalf("index.json stat error = %v", err)
    }

    second, err := manager.Create(context.Background(), "second prompt")
    if err != nil {
        t.Fatalf("Create() second error = %v", err)
    }
    if first.ID == second.ID {
        t.Fatalf("Create() produced duplicate id %q", first.ID)
    }

    resolved, err := manager.Resolve(context.Background(), first.ID)
    if err != nil {
        t.Fatalf("Resolve() error = %v", err)
    }
    if resolved.ID != first.ID || resolved.Path != first.Path || resolved.Title != first.Title {
        t.Fatalf("Resolve() = %#v, want first meta %#v", resolved, first)
    }

    if err := manager.Touch(context.Background(), first.ID); err != nil {
        t.Fatalf("Touch() error = %v", err)
    }
    listed, err := manager.List(context.Background())
    if err != nil {
        t.Fatalf("List() error = %v", err)
    }
    gotIDs := []string{listed[0].ID, listed[1].ID}
    wantIDs := []string{first.ID, second.ID}
    if !reflect.DeepEqual(gotIDs, wantIDs) {
        t.Fatalf("List() ids = %#v, want touched first before second", gotIDs)
    }
    if listed[0].UpdatedAt.Before(listed[1].UpdatedAt) {
        t.Fatalf("List() not sorted by updated_at desc: %#v", listed)
    }
}

func TestManagerCreateUsesUntitledSessionForBlankTitle(t *testing.T) {
    manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

    meta, err := manager.Create(context.Background(), " \n\t ")
    if err != nil {
        t.Fatalf("Create() error = %v", err)
    }
    if meta.Title != "untitled session" {
        t.Fatalf("Create() Title = %q, want untitled session", meta.Title)
    }
}

func TestManagerResolveMissingSessionReturnsError(t *testing.T) {
    manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

    _, err := manager.Resolve(context.Background(), "missing-session")
    if err == nil {
        t.Fatal("Resolve() error = nil, want missing session error")
    }
    if !strings.Contains(err.Error(), "missing-session") {
        t.Fatalf("Resolve() error = %q, want missing id context", err.Error())
    }
}

func TestManagerListMissingIndexReturnsEmptyList(t *testing.T) {
    manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

    metas, err := manager.List(context.Background())
    if err != nil {
        t.Fatalf("List() error = %v", err)
    }
    if len(metas) != 0 {
        t.Fatalf("List() len = %d, want 0", len(metas))
    }
}

func TestManagerMalformedIndexReturnsPathError(t *testing.T) {
    root := filepath.Join(t.TempDir(), "sessions")
    if err := os.MkdirAll(root, 0o700); err != nil {
        t.Fatalf("MkdirAll() error = %v", err)
    }
    indexPath := filepath.Join(root, "index.json")
    if err := os.WriteFile(indexPath, []byte("{not-json}"), 0o600); err != nil {
        t.Fatalf("write malformed index: %v", err)
    }

    _, err := NewManager(root).List(context.Background())
    if err == nil {
        t.Fatal("List() error = nil, want malformed index error")
    }
    if !strings.Contains(err.Error(), indexPath) {
        t.Fatalf("List() error = %q, want index path context", err.Error())
    }
}

func TestManagerTouchMissingSessionReturnsError(t *testing.T) {
    manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

    err := manager.Touch(context.Background(), "missing-session")
    if err == nil {
        t.Fatal("Touch() error = nil, want missing session error")
    }
    if !strings.Contains(err.Error(), "missing-session") {
        t.Fatalf("Touch() error = %q, want missing id context", err.Error())
    }
}

func TestManagerTimestampsAreUTC(t *testing.T) {
    manager := NewManager(filepath.Join(t.TempDir(), "sessions"))

    meta, err := manager.Create(context.Background(), "prompt")
    if err != nil {
        t.Fatalf("Create() error = %v", err)
    }
    if meta.CreatedAt.Location() != time.UTC || meta.UpdatedAt.Location() != time.UTC {
        t.Fatalf("timestamps locations = %v / %v, want UTC", meta.CreatedAt.Location(), meta.UpdatedAt.Location())
    }
}
```

- [ ] **Step 2: Run RED**

Run:

```bash
go test ./internal/session -run 'TestManager'
```

Expected: compile failure because `NewManager`, `Manager`, and `SessionMeta` do not exist.

---

## Task 2: GREEN Implement Manager Storage

**Files:**
- Create: `internal/session/manager.go`
- Modify if needed: `internal/session/manager_test.go`

- [ ] **Step 1: Implement minimal Manager**

Create `internal/session/manager.go` with these responsibilities:

```go
package session

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "time"
)

const (
    defaultSessionManagerRoot = ".moe/sessions"
    sessionIndexFileName      = "index.json"
    untitledSessionTitle      = "untitled session"
)

// Manager 管理本地 session index 和 session 文件路径。
type Manager struct {
    root string
}

// SessionMeta 描述一个可恢复的本地 session。
type SessionMeta struct {
    // ID 是 CLI 用于 resume 的稳定 session 标识。
    ID string
    // Path 是可传给 Open 的 JSONL transcript 文件路径。
    Path string
    // Title 是创建 session 时从首个 prompt 生成的短标题。
    Title string
    // CreatedAt 是 manager 创建索引记录的 UTC 时间。
    CreatedAt time.Time
    // UpdatedAt 是最近一次 CLI 使用该 session 的 UTC 时间。
    UpdatedAt time.Time
}

type sessionIndex struct {
    Current  string             `json:"current,omitempty"`
    Sessions []sessionMetaEntry `json:"sessions"`
}

type sessionMetaEntry struct {
    ID        string    `json:"id"`
    Path      string    `json:"path"`
    Title     string    `json:"title"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

// NewManager 创建使用 root 目录的 session manager；root 为空时使用 .moe/sessions。
func NewManager(root string) *Manager {
    if strings.TrimSpace(root) == "" {
        root = defaultSessionManagerRoot
    }
    return &Manager{root: root}
}

// Create 创建一条索引记录并返回可传给 Open 的 session 文件路径。
func (m *Manager) Create(ctx context.Context, title string) (SessionMeta, error) {
    if ctx == nil {
        ctx = context.Background()
    }
    if err := ctx.Err(); err != nil {
        return SessionMeta{}, err
    }
    index, err := m.loadIndex()
    if err != nil {
        return SessionMeta{}, err
    }
    now := time.Now().UTC()
    id, err := newSessionID(now)
    if err != nil {
        return SessionMeta{}, err
    }
    meta := sessionMetaEntry{
        ID:        id,
        Path:      filepath.Join(m.root, id+".jsonl"),
        Title:     normalizeSessionTitle(title),
        CreatedAt: now,
        UpdatedAt: now,
    }
    index.Current = id
    index.Sessions = append(index.Sessions, meta)
    if err := m.saveIndex(index); err != nil {
        return SessionMeta{}, err
    }
    return meta.toSessionMeta(), nil
}

// Resolve 根据 id 返回已有 session 元数据。
func (m *Manager) Resolve(ctx context.Context, id string) (SessionMeta, error) {
    if ctx == nil {
        ctx = context.Background()
    }
    if err := ctx.Err(); err != nil {
        return SessionMeta{}, err
    }
    index, err := m.loadIndex()
    if err != nil {
        return SessionMeta{}, err
    }
    for _, meta := range index.Sessions {
        if meta.ID == id {
            return meta.toSessionMeta(), nil
        }
    }
    return SessionMeta{}, fmt.Errorf("session %q not found", id)
}

// List 按 updated_at 倒序返回 session 列表。
func (m *Manager) List(ctx context.Context) ([]SessionMeta, error) {
    if ctx == nil {
        ctx = context.Background()
    }
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    index, err := m.loadIndex()
    if err != nil {
        return nil, err
    }
    metas := make([]SessionMeta, 0, len(index.Sessions))
    for _, entry := range index.Sessions {
        metas = append(metas, entry.toSessionMeta())
    }
    sort.SliceStable(metas, func(i, j int) bool {
        return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
    })
    return metas, nil
}

// Touch 更新 session 的 updated_at，并将它设为 current。
func (m *Manager) Touch(ctx context.Context, id string) error {
    if ctx == nil {
        ctx = context.Background()
    }
    if err := ctx.Err(); err != nil {
        return err
    }
    index, err := m.loadIndex()
    if err != nil {
        return err
    }
    for i := range index.Sessions {
        if index.Sessions[i].ID == id {
            index.Sessions[i].UpdatedAt = time.Now().UTC()
            index.Current = id
            return m.saveIndex(index)
        }
    }
    return fmt.Errorf("session %q not found", id)
}
```

Add helpers in the same file:

```go
func (m *Manager) indexPath() string {
    return filepath.Join(m.root, sessionIndexFileName)
}

func (m *Manager) loadIndex() (sessionIndex, error) {
    data, err := os.ReadFile(m.indexPath())
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return sessionIndex{}, nil
        }
        return sessionIndex{}, fmt.Errorf("read session index %q: %w", m.indexPath(), err)
    }
    var index sessionIndex
    if err := json.Unmarshal(data, &index); err != nil {
        return sessionIndex{}, fmt.Errorf("parse session index %q: %w", m.indexPath(), err)
    }
    return index, nil
}

func (m *Manager) saveIndex(index sessionIndex) error {
    if err := os.MkdirAll(m.root, 0o700); err != nil {
        return fmt.Errorf("create session index dir: %w", err)
    }
    data, err := json.MarshalIndent(index, "", "  ")
    if err != nil {
        return fmt.Errorf("encode session index: %w", err)
    }
    data = append(data, '\n')
    if err := os.WriteFile(m.indexPath(), data, 0o600); err != nil {
        return fmt.Errorf("write session index %q: %w", m.indexPath(), err)
    }
    return nil
}

func normalizeSessionTitle(title string) string {
    line := strings.TrimSpace(strings.Split(strings.ReplaceAll(title, "\r\n", "\n"), "\n")[0])
    if line == "" {
        return untitledSessionTitle
    }
    if len(line) > 80 {
        return line[:80]
    }
    return line
}

func newSessionID(now time.Time) (string, error) {
    var random [3]byte
    if _, err := rand.Read(random[:]); err != nil {
        return "", fmt.Errorf("generate session id: %w", err)
    }
    return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(random[:]), nil
}

func (e sessionMetaEntry) toSessionMeta() SessionMeta {
    return SessionMeta{
        ID:        e.ID,
        Path:      e.Path,
        Title:     e.Title,
        CreatedAt: e.CreatedAt.UTC(),
        UpdatedAt: e.UpdatedAt.UTC(),
    }
}
```

If tests need deterministic ordering after quick creates, insert a short `time.Sleep(time.Millisecond)` in the test before `Touch`, not production code.

- [ ] **Step 2: Run GREEN**

Run:

```bash
go test ./internal/session -run 'TestManager'
```

Expected: all manager tests pass.

- [ ] **Step 3: Run focused package tests**

Run:

```bash
go test ./internal/session
```

Expected: session package passes; existing `Session.Open` tests still pass.

---

## Task 3: RED CLI Flag Parsing and Conflict Validation

**Files:**
- Modify: `cmd/cli/main_test.go`
- Later implementation target: `cmd/cli/main.go`

- [ ] **Step 1: Add parsing and conflict tests**

Add tests near existing `TestParseCLIOptions...` tests:

```go
func TestParseCLIOptionsAcceptsSessionLifecycleFlags(t *testing.T) {
    got, err := parseCLIOptions([]string{"--new-session", "first", "prompt"})
    if err != nil {
        t.Fatalf("parseCLIOptions() --new-session error = %v", err)
    }
    if !got.newSession {
        t.Fatal("newSession = false, want true")
    }
    if strings.Join(got.promptArgs, " ") != "first prompt" {
        t.Fatalf("promptArgs = %#v, want first prompt", got.promptArgs)
    }

    got, err = parseCLIOptions([]string{"--resume", "20260708-abc123", "next", "prompt"})
    if err != nil {
        t.Fatalf("parseCLIOptions() --resume error = %v", err)
    }
    if got.resumeSessionID != "20260708-abc123" {
        t.Fatalf("resumeSessionID = %q, want 20260708-abc123", got.resumeSessionID)
    }
    if strings.Join(got.promptArgs, " ") != "next prompt" {
        t.Fatalf("promptArgs = %#v, want next prompt", got.promptArgs)
    }

    got, err = parseCLIOptions([]string{"--list-sessions"})
    if err != nil {
        t.Fatalf("parseCLIOptions() --list-sessions error = %v", err)
    }
    if !got.listSessions {
        t.Fatal("listSessions = false, want true")
    }
}

func TestParseCLIOptionsRejectsConflictingSessionLifecycleFlags(t *testing.T) {
    tests := []struct {
        name string
        args []string
    }{
        {name: "session and new", args: []string{"--session", "manual.jsonl", "--new-session", "prompt"}},
        {name: "session and resume", args: []string{"--session", "manual.jsonl", "--resume", "abc", "prompt"}},
        {name: "new and resume", args: []string{"--new-session", "--resume", "abc", "prompt"}},
        {name: "list and prompt", args: []string{"--list-sessions", "prompt"}},
        {name: "list and interactive", args: []string{"--list-sessions", "--interactive"}},
        {name: "list and session", args: []string{"--list-sessions", "--session", "manual.jsonl"}},
        {name: "list and new", args: []string{"--list-sessions", "--new-session"}},
        {name: "list and resume", args: []string{"--list-sessions", "--resume", "abc"}},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if _, err := parseCLIOptions(tt.args); err == nil {
                t.Fatalf("parseCLIOptions(%#v) error = nil, want conflict error", tt.args)
            }
        })
    }
}
```

- [ ] **Step 2: Run RED**

Run:

```bash
go test ./cmd/cli -run 'TestParseCLIOptions.*SessionLifecycle'
```

Expected: compile failure because `cliOptions.newSession`, `resumeSessionID`, and `listSessions` do not exist.

---

## Task 4: GREEN CLI Option Parsing

**Files:**
- Modify: `cmd/cli/main.go`

- [ ] **Step 1: Extend cliOptions**

Add fields to `cliOptions`:

```go
// newSession 表示创建 manager-managed session 并用本轮 prompt 作为标题来源。
newSession bool
// resumeSessionID 是从 manager index 恢复的 session id。
resumeSessionID string
// listSessions 表示只列出 manager-managed sessions，不创建 Agent 或读取 Provider。
listSessions bool
```

- [ ] **Step 2: Register flags**

In `parseCLIOptions`, add:

```go
flags.BoolVar(&opts.newSession, "new-session", false, "create a managed session")
flags.StringVar(&opts.resumeSessionID, "resume", "", "managed session id to resume")
flags.BoolVar(&opts.listSessions, "list-sessions", false, "list managed sessions")
```

- [ ] **Step 3: Add validation helper**

Add helper in `cmd/cli/main.go`:

```go
func validateCLIOptions(opts cliOptions) error {
    hasManualSession := strings.TrimSpace(opts.sessionPath) != ""
    hasResume := strings.TrimSpace(opts.resumeSessionID) != ""

    if hasManualSession && opts.newSession {
        return fmt.Errorf("--session and --new-session are mutually exclusive")
    }
    if hasManualSession && hasResume {
        return fmt.Errorf("--session and --resume are mutually exclusive")
    }
    if opts.newSession && hasResume {
        return fmt.Errorf("--new-session and --resume are mutually exclusive")
    }
    if opts.listSessions {
        if len(opts.promptArgs) > 0 || opts.interactive || hasManualSession || opts.newSession || hasResume {
            return fmt.Errorf("--list-sessions cannot be combined with prompt, --interactive, --session, --new-session, or --resume")
        }
    }
    return nil
}
```

Call it at the end of `parseCLIOptions` after `opts.promptArgs = flags.Args()`:

```go
if err := validateCLIOptions(opts); err != nil {
    return cliOptions{}, err
}
```

- [ ] **Step 4: Run GREEN**

Run:

```bash
go test ./cmd/cli -run 'TestParseCLIOptions.*SessionLifecycle|TestParseCLIOptionsAcceptsSessionFlag'
```

Expected: CLI option parsing tests pass, existing `--session` test still passes.

---

## Task 5: RED CLI List Sessions Without Provider

**Files:**
- Modify: `cmd/cli/main_test.go`
- Later implementation target: `cmd/cli/main.go`

- [ ] **Step 1: Add list formatting test**

Add test:

```go
func TestFormatSessionListOutput(t *testing.T) {
    metas := []session.SessionMeta{
        {
            ID:        "20260708-150405-a1b2c3",
            Title:     "use calculator to compute 13 * 7",
            UpdatedAt: time.Date(2026, 7, 8, 15, 4, 5, 0, time.UTC),
        },
        {
            ID:        "20260708-151210-d4e5f6",
            Title:     "second prompt",
            UpdatedAt: time.Date(2026, 7, 8, 15, 12, 10, 0, time.UTC),
        },
    }

    got := formatSessionListOutput(metas)
    want := "20260708-150405-a1b2c3  2026-07-08T15:04:05Z  use calculator to compute 13 * 7\n" +
        "20260708-151210-d4e5f6  2026-07-08T15:12:10Z  second prompt\n"
    if got != want {
        t.Fatalf("formatSessionListOutput() = %q, want %q", got, want)
    }
}

func TestFormatSessionListOutputEmpty(t *testing.T) {
    if got := formatSessionListOutput(nil); got != "" {
        t.Fatalf("formatSessionListOutput(nil) = %q, want empty", got)
    }
}
```

Add `time` import to `cmd/cli/main_test.go` if not already present.

- [ ] **Step 2: Run RED**

Run:

```bash
go test ./cmd/cli -run 'TestFormatSessionListOutput'
```

Expected: compile failure because `formatSessionListOutput` does not exist.

---

## Task 6: GREEN CLI List Formatting and Early List Path

**Files:**
- Modify: `cmd/cli/main.go`

- [ ] **Step 1: Add manager root constant**

Add near existing CLI constants:

```go
const defaultCLISessionRoot = ".moe/sessions"
```

- [ ] **Step 2: Add list formatting helper**

Add helper:

```go
func formatSessionListOutput(metas []session.SessionMeta) string {
    var builder strings.Builder
    for _, meta := range metas {
        builder.WriteString(meta.ID)
        builder.WriteString("  ")
        builder.WriteString(meta.UpdatedAt.UTC().Format(time.RFC3339))
        builder.WriteString("  ")
        builder.WriteString(meta.Title)
        builder.WriteByte('\n')
    }
    return builder.String()
}
```

Add `time` import to `cmd/cli/main.go`.

- [ ] **Step 3: Add list helper that does not need Provider**

Add:

```go
func runListSessions(ctx context.Context, output io.Writer, root string) error {
    metas, err := session.NewManager(root).List(ctx)
    if err != nil {
        return err
    }
    if _, err := io.WriteString(output, formatSessionListOutput(metas)); err != nil {
        return fmt.Errorf("write sessions: %w", err)
    }
    return nil
}
```

- [ ] **Step 4: Wire early list path in main**

In `main`, after parsing options and before `logger.NewDevelopmentFile`, add:

```go
if opts.listSessions {
    if err := runListSessions(context.Background(), os.Stdout, defaultCLISessionRoot); err != nil {
        log.Fatal(err)
    }
    return
}
```

This keeps `--list-sessions` independent from provider config and API keys.

- [ ] **Step 5: Run GREEN**

Run:

```bash
go test ./cmd/cli -run 'TestFormatSessionListOutput|TestParseCLIOptions.*SessionLifecycle'
```

Expected: tests pass.

---

## Task 7: RED CLI Managed New/Resume Reuses Transcript

**Files:**
- Modify: `cmd/cli/main_test.go`
- Later implementation target: `cmd/cli/main.go`

- [ ] **Step 1: Add a helper-level integration test**

Add test that avoids invoking `main` but exercises manager + CLI session creation helpers:

```go
func TestCLIManagedSessionNewAndResumeReuseTranscript(t *testing.T) {
    providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
    sessionRoot := filepath.Join(t.TempDir(), "sessions")

    firstOptions, err := parseCLIOptions([]string{
        "--config", providerConfigPath,
        "--new-session",
        "first prompt",
    })
    if err != nil {
        t.Fatalf("parseCLIOptions() first error = %v", err)
    }
    firstInput, err := readInput(firstOptions.promptArgs, strings.NewReader("ignored stdin"))
    if err != nil {
        t.Fatalf("readInput() first error = %v", err)
    }
    firstRunner, firstMeta, err := newCLISessionWithRoot(context.Background(), firstOptions, logger.NewNoop(), sessionRoot, firstInput)
    if err != nil {
        t.Fatalf("newCLISessionWithRoot() first error = %v", err)
    }
    firstOutput, err := collectRunOutput(firstRunner.Prompt(context.Background(), firstInput))
    if err != nil {
        t.Fatalf("collectRunOutput() first error = %v", err)
    }
    if firstOutput.Answer != "13 * 7 = 91" {
        t.Fatalf("first answer = %q, want 13 * 7 = 91", firstOutput.Answer)
    }
    if err := touchManagedSession(context.Background(), firstMeta); err != nil {
        t.Fatalf("touchManagedSession() first error = %v", err)
    }

    secondOptions, err := parseCLIOptions([]string{
        "--config", providerConfigPath,
        "--resume", firstMeta.ID,
        "second prompt",
    })
    if err != nil {
        t.Fatalf("parseCLIOptions() second error = %v", err)
    }
    secondInput, err := readInput(secondOptions.promptArgs, strings.NewReader("ignored stdin"))
    if err != nil {
        t.Fatalf("readInput() second error = %v", err)
    }
    secondRunner, secondMeta, err := newCLISessionWithRoot(context.Background(), secondOptions, logger.NewNoop(), sessionRoot, secondInput)
    if err != nil {
        t.Fatalf("newCLISessionWithRoot() second error = %v", err)
    }
    if secondMeta.ID != firstMeta.ID {
        t.Fatalf("resumed meta ID = %q, want %q", secondMeta.ID, firstMeta.ID)
    }
    if got := collectUserPrompts(secondRunner.Messages()); !reflect.DeepEqual(got, []string{"first prompt"}) {
        t.Fatalf("reopened user prompts before second run = %#v, want first prompt", got)
    }
    secondOutput, err := collectRunOutput(secondRunner.Prompt(context.Background(), secondInput))
    if err != nil {
        t.Fatalf("collectRunOutput() second error = %v", err)
    }
    if secondOutput.Answer != "13 * 7 = 91" {
        t.Fatalf("second answer = %q, want 13 * 7 = 91", secondOutput.Answer)
    }
}
```

This test assumes helpers return managed session metadata. It may need minor signature adjustment during implementation, but the invariant must remain: `--new-session` creates an indexed path and `--resume <id>` opens the same durable transcript.

- [ ] **Step 2: Run RED**

Run:

```bash
go test ./cmd/cli -run TestCLIManagedSessionNewAndResumeReuseTranscript
```

Expected: compile failure because `newCLISessionWithRoot` and `touchManagedSession` do not exist.

---

## Task 8: GREEN CLI Managed Session Resolution

**Files:**
- Modify: `cmd/cli/main.go`

- [ ] **Step 1: Introduce managed metadata wrapper**

Add in `cmd/cli/main.go`:

```go
type cliManagedSession struct {
    manager *session.Manager
    id      string
}
```

- [ ] **Step 2: Keep existing newCLISession as wrapper**

Change existing `newCLISession` to call a root-aware helper:

```go
func newCLISession(ctx context.Context, opts cliOptions, appLogger logger.Logger) (*session.Session, error) {
    runner, _, err := newCLISessionWithRoot(ctx, opts, appLogger, defaultCLISessionRoot, strings.Join(opts.promptArgs, " "))
    return runner, err
}
```

- [ ] **Step 3: Add root-aware helper**

Add:

```go
func newCLISessionWithRoot(ctx context.Context, opts cliOptions, appLogger logger.Logger, sessionRoot string, title string) (*session.Session, *cliManagedSession, error) {
    cfg := session.Config{
        ProviderConfigPath: opts.configPath,
        ProviderName:       opts.providerName,
        Logger:             appLogger,
    }
    if strings.TrimSpace(opts.sessionPath) != "" {
        runner, err := session.Open(ctx, cfg, opts.sessionPath)
        return runner, nil, err
    }

    manager := session.NewManager(sessionRoot)
    if opts.newSession {
        meta, err := manager.Create(ctx, title)
        if err != nil {
            return nil, nil, err
        }
        runner, err := session.Open(ctx, cfg, meta.Path)
        if err != nil {
            return nil, nil, err
        }
        return runner, &cliManagedSession{manager: manager, id: meta.ID}, nil
    }

    if strings.TrimSpace(opts.resumeSessionID) != "" {
        meta, err := manager.Resolve(ctx, opts.resumeSessionID)
        if err != nil {
            return nil, nil, err
        }
        runner, err := session.Open(ctx, cfg, meta.Path)
        if err != nil {
            return nil, nil, err
        }
        return runner, &cliManagedSession{manager: manager, id: meta.ID}, nil
    }

    runner, err := session.New(ctx, cfg)
    return runner, nil, err
}
```

- [ ] **Step 4: Add touch helper**

Add:

```go
func touchManagedSession(ctx context.Context, managed *cliManagedSession) error {
    if managed == nil {
        return nil
    }
    return managed.manager.Touch(ctx, managed.id)
}
```

- [ ] **Step 5: Wire touch in main for non-interactive and interactive paths**

In `main`, compute input before creating a managed new session title for non-interactive mode. The simplest safe shape:

1. Parse options.
2. Early return for `--list-sessions`.
3. For non-interactive, call `readInput` before `newCLISessionWithRoot` so `--new-session` title uses the actual prompt.
4. For interactive `--new-session`, use `"interactive session"` as the title.
5. After successful run/interactive loop, call `touchManagedSession`.

Keep behavior unchanged for no session flags and manual `--session`.

- [ ] **Step 6: Run GREEN**

Run:

```bash
go test ./cmd/cli -run 'TestCLIManagedSessionNewAndResumeReuseTranscript|TestCLISeparateSessionInstancesReuseTranscriptFromSessionFile'
```

Expected: managed lifecycle test passes and manual `--session` behavior remains green.

---

## Task 9: RED/GREEN CLI List Sessions Uses Manager and Avoids Provider

**Files:**
- Modify: `cmd/cli/main_test.go`
- Modify: `cmd/cli/main.go` if needed

- [ ] **Step 1: Add list helper integration test**

Add:

```go
func TestRunListSessionsUsesManagerIndex(t *testing.T) {
    sessionRoot := filepath.Join(t.TempDir(), "sessions")
    manager := session.NewManager(sessionRoot)
    first, err := manager.Create(context.Background(), "first prompt")
    if err != nil {
        t.Fatalf("Create() first error = %v", err)
    }
    second, err := manager.Create(context.Background(), "second prompt")
    if err != nil {
        t.Fatalf("Create() second error = %v", err)
    }
    if err := manager.Touch(context.Background(), first.ID); err != nil {
        t.Fatalf("Touch() first error = %v", err)
    }

    var output bytes.Buffer
    if err := runListSessions(context.Background(), &output, sessionRoot); err != nil {
        t.Fatalf("runListSessions() error = %v", err)
    }

    got := output.String()
    if !strings.Contains(got, first.ID+"  ") || !strings.Contains(got, second.ID+"  ") {
        t.Fatalf("runListSessions() output = %q, want both ids %q and %q", got, first.ID, second.ID)
    }
    if strings.Index(got, first.ID) > strings.Index(got, second.ID) {
        t.Fatalf("runListSessions() output = %q, want touched first before second", got)
    }
}

func TestRunListSessionsWithMissingIndexPrintsNothing(t *testing.T) {
    var output bytes.Buffer
    if err := runListSessions(context.Background(), &output, filepath.Join(t.TempDir(), "sessions")); err != nil {
        t.Fatalf("runListSessions() error = %v", err)
    }
    if output.String() != "" {
        t.Fatalf("runListSessions() output = %q, want empty", output.String())
    }
}
```

- [ ] **Step 2: Run test**

Run:

```bash
go test ./cmd/cli -run 'TestRunListSessions'
```

Expected: pass if Task 6 implementation is complete; otherwise fix `runListSessions` only.

---

## Task 10: Focused Verification, Full Verification, Commit

**Files:** all changed files.

- [ ] **Step 1: Format changed Go files**

Run:

```bash
gofmt -w internal/session/manager.go internal/session/manager_test.go cmd/cli/main.go cmd/cli/main_test.go
```

- [ ] **Step 2: Focused tests**

Run:

```bash
go test ./internal/session ./cmd/cli
```

Expected: both packages pass.

- [ ] **Step 3: Full tests**

Run:

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 4: Vet**

Run:

```bash
go vet ./...
```

Expected: no output and exit 0.

- [ ] **Step 5: Race detector**

Run:

```bash
go test -race ./...
```

Expected: all packages pass.

- [ ] **Step 6: Inspect status**

Run:

```bash
git status --short
```

Expected changed files for lifecycle feature only:

```text
M cmd/cli/main.go
M cmd/cli/main_test.go
A internal/session/manager.go
A internal/session/manager_test.go
```

If unrelated previous-phase files are present, stop and separate commits before committing lifecycle work.

- [ ] **Step 7: Commit lifecycle feature**

Run:

```bash
git add cmd/cli/main.go cmd/cli/main_test.go internal/session/manager.go internal/session/manager_test.go
git commit -m "feat: add session lifecycle manager"
```

---

## Completion Definition

- `session.NewManager(root)` can create, resolve, list, and touch indexed sessions.
- `.moe/sessions/index.json` is the only manager index; JSONL transcript files remain owned by `session.Open`.
- CLI supports `--new-session`, `--resume <id>`, and `--list-sessions`.
- CLI keeps `--session <path>` behavior unchanged.
- `--list-sessions` does not load Provider config or require API keys.
- Conflict flags return errors.
- `go test ./...`, `go vet ./...`, and `go test -race ./...` pass.
