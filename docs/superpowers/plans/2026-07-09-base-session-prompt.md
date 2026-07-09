# Base and Session Prompts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split prompt configuration into project-level `BaseSystemPrompt` and managed-session `SessionPrompt`.

**Architecture:** Rename the current persisted session prompt path to `SessionPrompt`, add `BaseSystemPrompt` to agent/service run configuration, and combine both into one provider-facing system message. Public CLI/API session configuration switches from `system_prompt`/`--system-prompt` to `session_prompt`/`--session-prompt` with a clean cutover.

**Tech Stack:** Go, `testing`, existing fake/OpenAI-compatible provider tests, JSON session metadata, CLI `flag` package.

---

## File Structure

- Modify `internal/agent/agent.go`: rename option/config storage from `SystemPrompt` to `BaseSystemPrompt` and add `SessionPrompt`.
- Modify `internal/agent/configured.go`: pass `BaseSystemPrompt` and `SessionPrompt` into `agent.Options`.
- Modify `internal/agent/message.go`: replace `toLLMMessagesWithSystemPrompt` with combined base/session prompt helper.
- Modify `internal/agent/loop.go`: call the new prompt helper.
- Modify `internal/session/manager.go`: rename persisted JSON field to `session_prompt`.
- Modify `internal/session/manager_test.go`: verify `SessionPrompt` persistence and normalization.
- Modify `internal/application/service/session.go`: separate service-level `BaseSystemPrompt` from session-level `SessionPrompt`.
- Modify `internal/application/service/session_test.go`: verify provider request receives base + session prompt while transcript remains clean.
- Modify `internal/application/handler/session.go`: rename HTTP request/response fields to `session_prompt` / `has_session_prompt`.
- Modify `internal/application/router/router_test.go`: update HTTP contracts.
- Modify `cmd/cli/main.go`: rename CLI flag and session override storage to `--session-prompt`.
- Modify `cmd/cli/main_test.go`: update CLI parsing, persistence, resume, and validation tests.

---

### Task 1: Agent Prompt Composition

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/configured.go`
- Modify: `internal/agent/message.go`
- Modify: `internal/agent/loop.go`
- Test: existing/new tests under `internal/agent`

- [ ] **Step 1: Write failing agent tests**

Add tests in the existing `internal/agent` test files or a new `internal/agent/message_test.go`:

```go
func TestToLLMMessagesWithPromptsCombinesBaseAndSessionPrompt(t *testing.T) {
	messages := []Message{UserMessage{Content: "hello"}}

	got, err := toLLMMessagesWithPrompts(messages, " base prompt ", " session prompt ")
	if err != nil {
		t.Fatalf("toLLMMessagesWithPrompts() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("messages len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Role != llms.RoleSystem {
		t.Fatalf("first role = %q, want system", got[0].Role)
	}
	want := "base prompt\n\nSession prompt:\nsession prompt"
	if got[0].Content != want {
		t.Fatalf("system content = %q, want %q", got[0].Content, want)
	}
	if got[1].Role != llms.RoleUser || got[1].Content != "hello" {
		t.Fatalf("second message = %#v, want user hello", got[1])
	}
}

func TestToLLMMessagesWithPromptsUsesSessionPromptAlone(t *testing.T) {
	messages := []Message{UserMessage{Content: "hello"}}

	got, err := toLLMMessagesWithPrompts(messages, "", " session prompt ")
	if err != nil {
		t.Fatalf("toLLMMessagesWithPrompts() error = %v", err)
	}
	if got[0].Role != llms.RoleSystem || got[0].Content != "Session prompt:\nsession prompt" {
		t.Fatalf("first message = %#v, want session prompt system message", got[0])
	}
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/agent -run 'TestToLLMMessagesWithPrompts' -count=1
```

Expected: FAIL because `toLLMMessagesWithPrompts` does not exist.

- [ ] **Step 3: Implement minimal agent changes**

Update `internal/agent/agent.go`:

```go
type Options struct {
	Logger logger.Logger
	MaxSteps int
	BaseSystemPrompt string
	SessionPrompt string
}

type Agent struct {
	provider llms.Provider
	tools *tools.Registry
	model string
	logger logger.Logger
	maxSteps int
	baseSystemPrompt string
	sessionPrompt string
}
```

In `NewWithOptions`, trim and store both prompt fields:

```go
baseSystemPrompt := strings.TrimSpace(opts.BaseSystemPrompt)
sessionPrompt := strings.TrimSpace(opts.SessionPrompt)
```

Return:

```go
baseSystemPrompt: baseSystemPrompt,
sessionPrompt: sessionPrompt,
```

Update `internal/agent/configured.go` `Config`:

```go
BaseSystemPrompt string
SessionPrompt string
```

Pass through:

```go
BaseSystemPrompt: cfg.BaseSystemPrompt,
SessionPrompt: cfg.SessionPrompt,
```

Update `internal/agent/message.go` with:

```go
func toLLMMessagesWithPrompts(messages []Message, baseSystemPrompt string, sessionPrompt string) ([]llms.Message, error) {
	converted, err := toLLMMessages(messages)
	if err != nil {
		return nil, err
	}
	prompt := combineSystemPrompts(baseSystemPrompt, sessionPrompt)
	if prompt == "" {
		return converted, nil
	}
	out := make([]llms.Message, 0, len(converted)+1)
	out = append(out, llms.Message{Role: llms.RoleSystem, Content: prompt})
	out = append(out, converted...)
	return out, nil
}

func combineSystemPrompts(baseSystemPrompt string, sessionPrompt string) string {
	base := strings.TrimSpace(baseSystemPrompt)
	session := strings.TrimSpace(sessionPrompt)
	if base == "" && session == "" {
		return ""
	}
	if base == "" {
		return "Session prompt:\n" + session
	}
	if session == "" {
		return base
	}
	return base + "\n\nSession prompt:\n" + session
}
```

Update `internal/agent/loop.go`:

```go
llmMessages, err := toLLMMessagesWithPrompts(messages, a.baseSystemPrompt, a.sessionPrompt)
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./internal/agent -run 'TestToLLMMessagesWithPrompts' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/agent/agent.go internal/agent/configured.go internal/agent/message.go internal/agent/loop.go internal/agent/*_test.go
go test ./internal/agent -count=1
git add internal/agent
git commit -m "refactor: split agent prompt inputs"
```

---

### Task 2: Session Metadata Rename

**Files:**
- Modify: `internal/session/manager.go`
- Modify: `internal/session/manager_test.go`

- [ ] **Step 1: Write failing session metadata test**

Update the config persistence test in `internal/session/manager_test.go` to use `SessionPrompt` and assert JSON contains `session_prompt` rather than `system_prompt`:

```go
cfg := SessionConfig{ProviderName: "deepseek", SessionPrompt: "be concise", MaxSteps: 4}
```

After create/resolve, assert:

```go
if resolved.Config.SessionPrompt != "be concise" {
	t.Fatalf("SessionPrompt = %q, want be concise", resolved.Config.SessionPrompt)
}
```

Add a raw file assertion against the index file if the existing tests expose it; otherwise add this after create by reading the manager index JSON from the temp root:

```go
indexBytes, err := os.ReadFile(filepath.Join(manager.root, "index.json"))
if err != nil {
	t.Fatalf("ReadFile(index) error = %v", err)
}
if !strings.Contains(string(indexBytes), `"session_prompt":"be concise"`) {
	t.Fatalf("index JSON = %s, want session_prompt", string(indexBytes))
}
if strings.Contains(string(indexBytes), "system_prompt") {
	t.Fatalf("index JSON = %s, must not contain system_prompt", string(indexBytes))
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/session -run 'Test.*Config' -count=1
```

Expected: FAIL because `SessionPrompt` does not exist or JSON still uses `system_prompt`.

- [ ] **Step 3: Implement metadata rename**

Update `internal/session/manager.go`:

```go
type SessionConfig struct {
	ProviderName   string `json:"provider_name,omitempty"`
	SessionPrompt string `json:"session_prompt,omitempty"`
	MaxSteps      int    `json:"max_steps,omitempty"`
}
```

Update normalization:

```go
cfg.SessionPrompt = strings.TrimSpace(cfg.SessionPrompt)
```

Replace remaining `SystemPrompt` references in `internal/session` with `SessionPrompt`.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./internal/session -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/session/manager.go internal/session/manager_test.go
go test ./internal/session -count=1
git add internal/session
git commit -m "refactor: rename persisted session prompt"
```

---

### Task 3: Service Prompt Semantics

**Files:**
- Modify: `internal/application/service/session.go`
- Modify: `internal/application/service/session_test.go`

- [ ] **Step 1: Write failing service tests**

Update existing service tests to use `BaseSystemPrompt` and `SessionPrompt`:

```go
basePrompt := "project base prompt"
sessionPrompt := "private managed run session prompt"
svc, err := appservice.NewSessionService(appservice.SessionConfig{
	Store: data.NewMemorySessionStore(),
	ProviderConfigPath: configPath,
	ProviderName: "fake-local",
	BaseSystemPrompt: basePrompt,
})
created, err := svc.Create(ctx, "managed run", appservice.CreateOptions{SessionPrompt: sessionPrompt})
```

Provider assertion should expect:

```go
want := basePrompt + "\n\nSession prompt:\n" + sessionPrompt
assertProviderReceivedSystemPromptBeforeUser(t, captured.Messages, want, "hello from managed run")
```

Session detail assertion should check:

```go
if detail.Config.SessionPrompt != sessionPrompt {
	t.Fatalf("detail SessionPrompt = %q, want stored session prompt", detail.Config.SessionPrompt)
}
assertTranscriptDoesNotIncludeSystemPrompt(t, detail.Messages, basePrompt, "hello from managed run")
assertTranscriptDoesNotIncludeSystemPrompt(t, detail.Messages, sessionPrompt, "hello from managed run")
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/application/service -run 'TestSessionService.*Prompt' -count=1
```

Expected: FAIL because service config still uses `SystemPrompt`.

- [ ] **Step 3: Implement service changes**

Update `internal/application/service/session.go`:

```go
type SessionConfig struct {
	Store data.SessionStore
	ProviderConfigPath string
	ProviderName string
	BaseSystemPrompt string
	MaxSteps int
	Logger logger.Logger
}

type SessionService struct {
	store data.SessionStore
	config session.Config
	baseSystemPrompt string
}

type CreateOptions struct {
	ProviderName string
	SessionPrompt string
	MaxSteps int
}
```

In `NewSessionService`, store base prompt and pass into `session.Config`:

```go
BaseSystemPrompt: strings.TrimSpace(cfg.BaseSystemPrompt),
```

In `defaultSessionConfig`:

```go
cfg := session.SessionConfig{
	ProviderName: strings.TrimSpace(s.config.ProviderName),
	MaxSteps: s.config.MaxSteps,
}
if sessionPrompt := strings.TrimSpace(opts.SessionPrompt); sessionPrompt != "" {
	cfg.SessionPrompt = sessionPrompt
}
```

In `resolveRunConfig`:

```go
runCfg := s.config
runCfg.ProviderName = providerName
runCfg.BaseSystemPrompt = strings.TrimSpace(s.baseSystemPrompt)
if sessionPrompt := strings.TrimSpace(meta.Config.SessionPrompt); sessionPrompt != "" {
	runCfg.SessionPrompt = sessionPrompt
}
```

Replace all remaining service `SystemPrompt` session preference uses with `SessionPrompt`.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./internal/application/service -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/application/service/session.go internal/application/service/session_test.go
go test ./internal/application/service -count=1
git add internal/application/service
git commit -m "refactor: separate service base and session prompts"
```

---

### Task 4: CLI Public Rename

**Files:**
- Modify: `cmd/cli/main.go`
- Modify: `cmd/cli/main_test.go`

- [ ] **Step 1: Write failing CLI tests**

Update CLI tests so `parseCLIOptions` accepts `--session-prompt`:

```go
got, err := parseCLIOptions([]string{
	"--max-steps", "2",
	"--session-prompt", "answer like a careful calculator",
	"--new-session",
	"compute (2 + 3) * 4",
})
if err != nil {
	t.Fatalf("parseCLIOptions() error = %v", err)
}
if gotSessionPrompt := cliOptionString(t, got, "sessionPrompt"); gotSessionPrompt != "answer like a careful calculator" {
	t.Fatalf("sessionPrompt = %q, want stored flag value", gotSessionPrompt)
}
```

Update validation test to reject:

```go
{name: "list and session prompt", args: []string{"--list-sessions", "--session-prompt", "x"}},
```

Update managed session tests to assert `meta.Config.SessionPrompt` and no `SystemPrompt` use remains.

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./cmd/cli -run 'TestParseCLIOptionsAcceptsManagedPreferenceFlags|TestValidateCLIOptionsRejectsInvalidCombinations|Test.*SessionPrompt' -count=1
```

Expected: FAIL because `--session-prompt` is not defined and fields still use `systemPrompt`.

- [ ] **Step 3: Implement CLI rename**

Update `cliOptions`:

```go
sessionPrompt string
```

Update flag registration:

```go
flags.StringVar(&opts.sessionPrompt, "session-prompt", "", "session prompt for this run/session")
```

Update config construction:

```go
BaseSystemPrompt: "",
SessionPrompt: strings.TrimSpace(opts.sessionPrompt),
```

Update managed session return state:

```go
sessionPromptOverride string
```

Update touch logic:

```go
sessionPromptOverride := strings.TrimSpace(managed.sessionPromptOverride)
if sessionPromptOverride != "" {
	cfg.SessionPrompt = sessionPromptOverride
}
```

Update validation error text to mention `--session-prompt`.

Remove old `--system-prompt` flag and all `systemPrompt` CLI option names.

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./cmd/cli -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/cli/main.go cmd/cli/main_test.go
go test ./cmd/cli -count=1
git add cmd/cli
git commit -m "refactor: rename cli session prompt flag"
```

---

### Task 5: HTTP API Rename

**Files:**
- Modify: `internal/application/handler/session.go`
- Modify: `internal/application/router/router_test.go`

- [ ] **Step 1: Write failing HTTP tests**

Update router tests so create requests use:

```json
{"title":"demo","provider_name":"fake-local","session_prompt":"private session prompt","max_steps":4}
```

Update response assertions to use:

```go
HasSessionPrompt bool `json:"has_session_prompt"`
```

Assert response does not contain full text:

```go
if strings.Contains(body.String(), "private session prompt") {
	t.Fatalf("response leaks session prompt: %s", body.String())
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
go test ./internal/application/router -run 'TestRouterCreateAcceptsConfigOverridesWithoutExposingSessionPrompt|Test.*Config' -count=1
```

Expected: FAIL because handler still expects/responds with `system_prompt` naming.

- [ ] **Step 3: Implement HTTP rename**

Update `internal/application/handler/session.go`:

```go
type createSessionRequest struct {
	Input string `json:"input"`
	Title string `json:"title"`
	ProviderName string `json:"provider_name"`
	MaxSteps int `json:"max_steps"`
	SessionPrompt string `json:"session_prompt"`
}

type sessionConfigResponse struct {
	ProviderName string `json:"provider_name,omitempty"`
	MaxSteps int `json:"max_steps,omitempty"`
	HasSessionPrompt bool `json:"has_session_prompt,omitempty"`
}
```

Create call:

```go
appservice.CreateOptions{ProviderName: req.ProviderName, MaxSteps: req.MaxSteps, SessionPrompt: req.SessionPrompt}
```

Config summary:

```go
HasSessionPrompt: strings.TrimSpace(cfg.SessionPrompt) != "",
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
go test ./internal/application/router -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/application/handler/session.go internal/application/router/router_test.go
go test ./internal/application/router -count=1
git add internal/application/handler internal/application/router
git commit -m "refactor: rename http session prompt fields"
```

---

### Task 6: Cross-Package Cleanup and Verification

**Files:**
- Modify any remaining files found by search.
- Review: `docs/superpowers/specs/2026-07-09-base-session-prompt-design.md`

- [ ] **Step 1: Search for stale names**

Run searches:

```bash
# Use built-in grep tool in agent sessions, not shell grep.
```

Search terms:

```text
SystemPrompt
systemPrompt
system_prompt
--system-prompt
has_system_prompt
```

Expected allowed matches only in historical design docs or explicit migration notes. No production code/test public API should retain stale names unless referring to `BaseSystemPrompt`.

- [ ] **Step 2: Fix stale production references**

For each stale production reference, rename to one of:

```text
BaseSystemPrompt
SessionPrompt
sessionPrompt
session_prompt
--session-prompt
has_session_prompt
```

Do not edit historical specs unless they are actively misleading current implementation docs.

- [ ] **Step 3: Run focused package tests**

Run:

```bash
go test ./internal/agent ./internal/session ./internal/application/service ./internal/application/router ./cmd/cli -count=1
```

Expected: PASS.

- [ ] **Step 4: Run full suite**

Run:

```bash
go test -count=1 ./...
```

Expected: PASS.

- [ ] **Step 5: Commit cleanup if needed**

If Step 2 changed files:

```bash
gofmt -w <changed-go-files>
git add <changed-files>
git commit -m "chore: clean up prompt naming"
```

If no files changed, do not create an empty commit.

---

## Self-Review

- Spec coverage: plan covers BaseSystemPrompt, SessionPrompt, prompt combination, CLI/API rename, session metadata rename, `configs/` convention in the approved spec, and verification.
- Placeholder scan: no TBD/TODO placeholders remain; each task has exact files, tests, commands, and expected output.
- Type consistency: production names are `BaseSystemPrompt` for project-level config and `SessionPrompt` for session-level persisted config; public names are `--session-prompt`, `session_prompt`, and `has_session_prompt`.
