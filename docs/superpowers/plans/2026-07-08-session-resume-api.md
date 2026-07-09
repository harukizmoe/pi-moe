# Session Resume API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove and harden the existing HTTP/CLI session resume flow without adding a duplicate resume endpoint.

**Architecture:** Keep `POST /v1/sessions/:id/runs` as the HTTP resume+append operation and `--resume <id>` as the CLI resume operation. Add deterministic fake-provider history behavior so tests can prove restored transcript state. Add a small not-found boundary instead of handler string matching.

**Tech Stack:** Go, Gin, append-only JSONL session storage, existing fake LLM provider, shell smoke scripts.

---

## File Structure

- Modify `internal/llms/fake.go`: add deterministic previous-result branch.
- Modify `internal/llms/fake_test.go`: cover previous-result behavior with and without history.
- Modify `internal/session/manager.go`: add minimal session not-found error helper.
- Modify `internal/application/handler/session.go`: use the helper for JSON 404 mapping.
- Modify `internal/application/service/session_test.go`: prove service resume restores history and detail returns both turns.
- Modify `internal/application/router/router_test.go`: prove HTTP multi-run resume, detail transcript ordering, `updated_at`, and missing-session JSON 404.
- Modify `cmd/cli/main_test.go`: prove managed CLI `--resume` appends to the same indexed session and updates `updated_at`.
- Modify `scripts/test-sse.sh`: add one HTTP resume smoke check after session detail.

---

### Task 1: Fake Provider History Contract

**Files:**
- Modify: `internal/llms/fake.go`
- Modify: `internal/llms/fake_test.go`

- [ ] **Step 1: Write failing tests**

Add tests to `internal/llms/fake_test.go`:

```go
func TestFakeProviderAnswersPreviousResultFromToolHistory(t *testing.T) {
	provider := &FakeProvider{model: "fake-tool-model"}
	events, err := provider.ChatStream(context.Background(), ChatRequest{Messages: []Message{
		{Role: RoleUser, Content: "use calculator to compute 13 * 7"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_fake_calculator", Type: "function", Function: ToolCallFunction{Name: "calculator", Arguments: `{"a":13,"b":7,"op":"mul"}`}}}},
		{Role: RoleTool, ToolCallID: "call_fake_calculator", Content: "91"},
		{Role: RoleAssistant, Content: "13 * 7 = 91"},
		{Role: RoleUser, Content: "what was the previous result?"},
	}})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	message := collectFakeDoneMessage(t, events)
	if message.Content != "previous result was 91" {
		t.Fatalf("previous result answer = %q, want previous result was 91", message.Content)
	}
}

func TestFakeProviderReportsMissingPreviousResultWithoutToolHistory(t *testing.T) {
	provider := &FakeProvider{model: "fake-tool-model"}
	events, err := provider.ChatStream(context.Background(), ChatRequest{Messages: []Message{
		{Role: RoleUser, Content: "what was the previous result?"},
	}})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	message := collectFakeDoneMessage(t, events)
	if message.Content != "no previous result found" {
		t.Fatalf("previous result answer = %q, want no previous result found", message.Content)
	}
}

func collectFakeDoneMessage(t *testing.T, events <-chan ChatStreamEvent) Message {
	t.Helper()
	for event := range events {
		if event.Type == ChatStreamEventTypeDone {
			return event.Message
		}
	}
	t.Fatal("stream ended without done event")
	return Message{}
}
```

- [ ] **Step 2: Verify RED**

Run:

```bash
go test ./internal/llms -run 'TestFakeProvider(AnswersPreviousResultFromToolHistory|ReportsMissingPreviousResultWithoutToolHistory)' -count=1
```

Expected: FAIL because fake provider currently asks for calculator instead of answering previous-result prompts.

- [ ] **Step 3: Implement minimal fake provider branch**

In `internal/llms/fake.go`, add `strings` import and update `fakeChatMessage` before the existing tool-result completion branch:

```go
func (p *FakeProvider) fakeChatMessage(req ChatRequest) Message {
	if asksPreviousResult(req.Messages) {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			msg := req.Messages[i]
			if msg.Role == RoleTool && strings.TrimSpace(msg.Content) != "" {
				return Message{Role: RoleAssistant, Content: "previous result was " + msg.Content}
			}
		}
		return Message{Role: RoleAssistant, Content: "no previous result found"}
	}

	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == RoleTool {
			return Message{Role: RoleAssistant, Content: "13 * 7 = " + msg.Content}
		}
	}

	return Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_fake_calculator", Type: "function", Function: ToolCallFunction{Name: "calculator", Arguments: `{"a":13,"b":7,"op":"mul"}`}}}}
}

func asksPreviousResult(messages []Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != RoleUser {
			continue
		}
		content := strings.ToLower(msg.Content)
		return strings.Contains(content, "previous result") || strings.Contains(content, "上一轮结果")
	}
	return false
}
```

- [ ] **Step 4: Verify GREEN**

Run:

```bash
go test ./internal/llms -run 'TestFakeProvider(AnswersPreviousResultFromToolHistory|ReportsMissingPreviousResultWithoutToolHistory)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/llms/fake.go internal/llms/fake_test.go
git commit -m "test: prove fake provider resume history"
```

---

### Task 2: Not Found Boundary

**Files:**
- Modify: `internal/session/manager.go`
- Modify: `internal/application/handler/session.go`
- Modify: `internal/application/router/router_test.go`

- [ ] **Step 1: Write failing route assertion if needed**

Ensure `TestRouterGetMissingSessionReturnsJSON404` in `internal/application/router/router_test.go` expects JSON 404 with `session "missing" not found`. This test already exists from the detail endpoint work and should stay focused on behavior.

- [ ] **Step 2: Add minimal not-found type and helper**

In `internal/session/manager.go`, add `errors` import if missing and define:

```go
type notFoundError struct {
	id string
}

func (e notFoundError) Error() string {
	return fmt.Sprintf("session %q not found", e.id)
}

// IsNotFound reports whether err means a session id is absent from the local index.
func IsNotFound(err error) bool {
	var target notFoundError
	return errors.As(err, &target)
}
```

Change `Resolve` final return to:

```go
return SessionMeta{}, notFoundError{id: id}
```

- [ ] **Step 3: Use helper in handler**

In `internal/application/handler/session.go`, import `harukizmoe/pimoe/internal/session` if needed and replace string matching in `Get` with:

```go
if session.IsNotFound(err) {
	writeError(ctx, http.StatusNotFound, err.Error())
	return
}
writeError(ctx, http.StatusInternalServerError, err.Error())
```

- [ ] **Step 4: Verify route behavior**

Run:

```bash
go test ./internal/application/router -run TestRouterGetMissingSessionReturnsJSON404 -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/session/manager.go internal/application/handler/session.go internal/application/router/router_test.go
git commit -m "fix: classify missing sessions"
```

---

### Task 3: Service and HTTP Resume Contracts

**Files:**
- Modify: `internal/application/service/session_test.go`
- Modify: `internal/application/router/router_test.go`
- Production code only if tests expose a real gap.

- [ ] **Step 1: Write service resume test**

Add to `internal/application/service/session_test.go`:

```go
func TestSessionServiceRunResumesExistingTranscriptForNextPrompt(t *testing.T) {
	ctx := context.Background()
	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	svc, err := appservice.NewSessionService(appservice.SessionConfig{Store: store, ProviderConfigPath: writeProvidersConfig(t), ProviderName: "fake-local"})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	created, err := svc.Create(ctx, "resume calculator")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := svc.Run(ctx, created.ID, "use calculator to compute 13 * 7"); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	second, err := svc.Run(ctx, created.ID, "what was the previous result?")
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if second.Answer != "previous result was 91" {
		t.Fatalf("second Run() Answer = %q, want previous result was 91", second.Answer)
	}
	detail, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(detail.Messages) != 6 {
		t.Fatalf("detail Messages len = %d, want 6: %#v", len(detail.Messages), detail.Messages)
	}
	if detail.Messages[4].Role != "user" || detail.Messages[4].Content != "what was the previous result?" {
		t.Fatalf("second user message = %#v", detail.Messages[4])
	}
	if detail.Messages[5].Role != "assistant" || detail.Messages[5].Content != "previous result was 91" {
		t.Fatalf("second assistant message = %#v", detail.Messages[5])
	}
}
```

- [ ] **Step 2: Verify service RED/GREEN**

Run:

```bash
go test ./internal/application/service -run TestSessionServiceRunResumesExistingTranscriptForNextPrompt -count=1
```

Expected before Task 1 implementation: FAIL. Expected after Task 1: PASS unless service resume has a real bug.

- [ ] **Step 3: Write router resume test**

Add to `internal/application/router/router_test.go`:

```go
func TestRouterRunsAppendToExistingSessionTranscript(t *testing.T) {
	handler := newTestRouter(t)
	created := createSession(t, handler, "resume calculator")

	first := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs", map[string]string{"input": "use calculator to compute 13 * 7"})
	assertStatus(t, first, http.StatusOK)
	firstBody := decodeJSON[runResponse](t, first)
	if firstBody.Answer != "13 * 7 = 91" {
		t.Fatalf("first answer = %q, want 13 * 7 = 91", firstBody.Answer)
	}

	second := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs", map[string]string{"input": "what was the previous result?"})
	assertStatus(t, second, http.StatusOK)
	secondBody := decodeJSON[runResponse](t, second)
	if secondBody.Answer != "previous result was 91" {
		t.Fatalf("second answer = %q, want previous result was 91", secondBody.Answer)
	}

	detailResp := httptest.NewRecorder()
	handler.ServeHTTP(detailResp, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID, nil))
	assertStatus(t, detailResp, http.StatusOK)
	detail := decodeJSON[sessionDetailResponse](t, detailResp)
	if len(detail.Messages) != 6 {
		t.Fatalf("detail messages len = %d, want 6: %#v", len(detail.Messages), detail.Messages)
	}
	if detail.Messages[4].Role != "user" || detail.Messages[4].Content != "what was the previous result?" {
		t.Fatalf("second user message = %#v", detail.Messages[4])
	}
	if detail.Messages[5].Role != "assistant" || detail.Messages[5].Content != "previous result was 91" {
		t.Fatalf("second assistant message = %#v", detail.Messages[5])
	}
	if detail.UpdatedAt == created.UpdatedAt {
		t.Fatalf("updated_at did not change after resumed runs: %q", detail.UpdatedAt)
	}
}
```

- [ ] **Step 4: Verify router test**

Run:

```bash
go test ./internal/application/router -run TestRouterRunsAppendToExistingSessionTranscript -count=1
```

Expected: PASS after Task 1.

- [ ] **Step 5: Commit**

```bash
git add internal/application/service/session_test.go internal/application/router/router_test.go
git commit -m "test: prove http session resume"
```

---

### Task 4: CLI Resume Contract

**Files:**
- Modify: `cmd/cli/main_test.go`
- Production code only if the test exposes a real gap.

- [ ] **Step 1: Write CLI resume test**

Add to `cmd/cli/main_test.go` near existing session lifecycle tests:

```go
func TestCLIManagedResumeAppendsToIndexedSession(t *testing.T) {
	ctx := context.Background()
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	root := filepath.Join(t.TempDir(), "sessions")
	newOpts, err := parseCLIOptions([]string{"--config", providerConfigPath, "--provider", "fake-local", "--new-session", "use calculator to compute 13 * 7"})
	if err != nil {
		t.Fatalf("parse new options: %v", err)
	}
	firstRunner, managed, err := newCLISessionWithRoot(ctx, newOpts, logger.NewNoop(), root, strings.Join(newOpts.promptArgs, " "))
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() first error = %v", err)
	}
	firstOutput, err := collectRunOutput(firstRunner.Prompt(ctx, "use calculator to compute 13 * 7"))
	if err != nil {
		t.Fatalf("first collectRunOutput() error = %v", err)
	}
	if firstOutput.Answer != "13 * 7 = 91" {
		t.Fatalf("first answer = %q, want 13 * 7 = 91", firstOutput.Answer)
	}
	if err := touchManagedSession(ctx, managed); err != nil {
		t.Fatalf("first touch error = %v", err)
	}
	manager := session.NewManager(root)
	before, err := manager.Resolve(ctx, managed.ID)
	if err != nil {
		t.Fatalf("Resolve() before error = %v", err)
	}
	time.Sleep(time.Nanosecond)

	resumeOpts, err := parseCLIOptions([]string{"--config", providerConfigPath, "--provider", "fake-local", "--resume", managed.ID, "what was the previous result?"})
	if err != nil {
		t.Fatalf("parse resume options: %v", err)
	}
	secondRunner, resumed, err := newCLISessionWithRoot(ctx, resumeOpts, logger.NewNoop(), root, strings.Join(resumeOpts.promptArgs, " "))
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() resume error = %v", err)
	}
	if resumed == nil || resumed.ID != managed.ID {
		t.Fatalf("resumed managed session = %#v, want id %q", resumed, managed.ID)
	}
	secondOutput, err := collectRunOutput(secondRunner.Prompt(ctx, "what was the previous result?"))
	if err != nil {
		t.Fatalf("second collectRunOutput() error = %v", err)
	}
	if secondOutput.Answer != "previous result was 91" {
		t.Fatalf("second answer = %q, want previous result was 91", secondOutput.Answer)
	}
	if err := touchManagedSession(ctx, resumed); err != nil {
		t.Fatalf("second touch error = %v", err)
	}
	after, err := manager.Resolve(ctx, managed.ID)
	if err != nil {
		t.Fatalf("Resolve() after error = %v", err)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated_at = %s, want after %s", after.UpdatedAt, before.UpdatedAt)
	}
	messages, err := session.LoadMessages(after.Path)
	if err != nil {
		t.Fatalf("LoadMessages() error = %v", err)
	}
	if len(messages) != 6 {
		t.Fatalf("messages len = %d, want 6: %#v", len(messages), messages)
	}
}
```

- [ ] **Step 2: Verify CLI test**

Run:

```bash
go test ./cmd/cli -run TestCLIManagedResumeAppendsToIndexedSession -count=1
```

Expected: PASS after Task 1 unless CLI resume has a real gap.

- [ ] **Step 3: Commit**

```bash
git add cmd/cli/main_test.go
git commit -m "test: prove cli managed resume"
```

---

### Task 5: Smoke and Final Verification

**Files:**
- Modify: `scripts/test-sse.sh`

- [ ] **Step 1: Extend smoke script**

After the existing detail check in `scripts/test-sse.sh`, add:

```bash
resume_body="$(post_json "/v1/sessions/$run_session_id/runs" '{"input":"what was the previous result?"}')" || fail "resume run request failed"
contains "$resume_body" "previous result was 91" || fail "resume run did not use restored transcript" "$resume_body"
ok "resume run used restored transcript"

resume_detail_body="$(get_json "/v1/sessions/$run_session_id")" || fail "resume detail request failed"
contains "$resume_detail_body" "previous result was 91" || fail "resume detail missing second answer" "$resume_detail_body"
ok "session detail returned resumed transcript"
```

- [ ] **Step 2: Verify syntax**

Run:

```bash
bash -n scripts/test-sse.sh
```

Expected: no output, exit 0.

- [ ] **Step 3: Run focused tests**

Run:

```bash
go test -count=1 ./internal/llms ./internal/application/service ./internal/application/router ./cmd/cli ./cmd/server
```

Expected: all packages pass.

- [ ] **Step 4: Run full verification**

Run:

```bash
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./...
```

Expected: all pass.

- [ ] **Step 5: Run smoke against fake server**

Run:

```bash
go run ./cmd/server -addr :18085 -provider fake
BASE_URL=http://localhost:18085 scripts/test-sse.sh
```

Expected smoke output includes:

```text
[OK] resume run used restored transcript
[OK] session detail returned resumed transcript
[OK] manual SSE smoke test passed
```

- [ ] **Step 6: Commit**

```bash
git add scripts/test-sse.sh
git commit -m "test: cover resume smoke"
```

- [ ] **Step 7: Final review**

Request code review for the branch. Critical and Important findings must be fixed before completion.
