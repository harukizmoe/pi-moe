# OpenAI Provider Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the OpenAI-compatible provider error boundary with deterministic local tests only.

**Architecture:** Keep the existing provider-neutral `llms.Provider` interface and harden only the OpenAI-compatible adapter. Add small helpers inside `internal/llms/openai_compatible.go` for status-body excerpts, request error wrapping, stream error wording, and final tool-call validation. Tests use `httptest.Server` and never call real providers.

**Tech Stack:** Go, `net/http`, `httptest`, SSE parsing through existing `ChatStream`, `go test`, `go vet`.

---

## File Structure

- Modify `internal/llms/openai_compatible.go`
  - Keep `OpenAICompatibleProvider` API unchanged.
  - Add/adjust helper functions in this file only:
    - status body excerpt helper
    - stable request/status/stream error messages
    - final OpenAI stream tool-call validation helper
  - Preserve current compatibility: missing tool call `type` becomes `function`; `finish_reason` without `[DONE]` completes.

- Modify `internal/llms/openai_compatible_test.go`
  - Add focused tests for status errors, request errors, SSE errors, and final tool-call validation.
  - Strengthen existing assertions where they currently accept vague text such as `decode` or `ended without done`.
  - Reuse existing `writeSSE` and `collectChatStreamEvents` helpers.

No new packages, CLI flags, HTTP endpoints, or real-network scripts.

---

### Task 1: Status Error Boundary

**Files:**
- Modify: `internal/llms/openai_compatible_test.go`
- Modify: `internal/llms/openai_compatible.go`

- [ ] **Step 1: Write failing status error tests**

Add this test near `TestOpenAICompatibleProviderStatusErrorIncludesBodyExcerpt` in `internal/llms/openai_compatible_test.go`:

```go
func TestOpenAICompatibleProviderStatusErrorsAreStableAndRedacted(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       []string
		wantAbsent []string
	}{
		{
			name:       "json_body",
			statusCode: http.StatusUnauthorized,
			body:       `{"error":{"message":"bad key"}}`,
			want:       []string{"openai-compatible chat completions failed", "status 401", "bad key"},
			wantAbsent: []string{"secret-token", "Authorization"},
		},
		{
			name:       "plain_text_body",
			statusCode: http.StatusTooManyRequests,
			body:       "rate limited",
			want:       []string{"openai-compatible chat completions failed", "status 429", "rate limited"},
		},
		{
			name:       "empty_body",
			statusCode: http.StatusInternalServerError,
			body:       "",
			want:       []string{"openai-compatible chat completions failed", "status 500", "<empty body>"},
		},
		{
			name:       "long_body_is_truncated",
			statusCode: http.StatusBadGateway,
			body:       strings.Repeat("x", maxOpenAIErrorBodyBytes+50),
			want:       []string{"openai-compatible chat completions failed", "status 502", strings.Repeat("x", maxOpenAIErrorBodyBytes)},
			wantAbsent: []string{strings.Repeat("x", maxOpenAIErrorBodyBytes+1)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
					t.Fatalf("authorization = %q", got)
				}
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, APIKey: "secret-token", TimeoutSeconds: 3})
			if err != nil {
				t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
			}

			_, err = CollectChat(context.Background(), provider, ChatRequest{})
			if err == nil {
				t.Fatal("CollectChat() error = nil")
			}
			errorText := err.Error()
			for _, want := range tt.want {
				if !strings.Contains(errorText, want) {
					t.Fatalf("error %q missing %q", errorText, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(errorText, absent) {
					t.Fatalf("error %q leaked %q", errorText, absent)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/llms -run TestOpenAICompatibleProviderStatusErrorsAreStableAndRedacted -count=1
```

Expected: FAIL because current errors say `openai chat returned status ...` and empty bodies omit `<empty body>`.

- [ ] **Step 3: Implement stable status error text**

In `internal/llms/openai_compatible.go`, replace `openAIStatusError` with:

```go
func openAIStatusError(resp *http.Response) error {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxOpenAIErrorBodyBytes))
	if readErr != nil {
		return fmt.Errorf("openai-compatible chat completions failed: status %d: read error body: %w", resp.StatusCode, readErr)
	}
	bodyText := strings.TrimSpace(string(body))
	if bodyText == "" {
		bodyText = "<empty body>"
	}
	return fmt.Errorf("openai-compatible chat completions failed: status %d: %s", resp.StatusCode, bodyText)
}
```

Do not read response headers and do not include `p.apiKey` in any error.

- [ ] **Step 4: Run status tests**

Run:

```bash
go test ./internal/llms -run 'TestOpenAICompatibleProvider(StatusErrorIncludesBodyExcerpt|StatusErrorsAreStableAndRedacted)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/llms/openai_compatible.go internal/llms/openai_compatible_test.go
git commit -m "test: harden openai status errors"
```

---

### Task 2: Request Failure Boundary

**Files:**
- Modify: `internal/llms/openai_compatible_test.go`
- Modify: `internal/llms/openai_compatible.go`

- [ ] **Step 1: Write failing request failure tests**

Add this test after the status tests:

```go
func TestOpenAICompatibleProviderRequestErrorsKeepContextSemantics(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(50 * time.Millisecond)
		}))
		defer server.Close()

		provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 1})
		if err != nil {
			t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		_, err = CollectChat(ctx, provider, ChatRequest{})
		if err == nil {
			t.Fatal("CollectChat() error = nil")
		}
		if !strings.Contains(err.Error(), "openai-compatible chat completions request") {
			t.Fatalf("error missing request stage: %v", err)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want context deadline semantics", err)
		}
	})

	t.Run("canceled", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("server should not receive canceled request")
		}))
		defer server.Close()

		provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 3})
		if err != nil {
			t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = CollectChat(ctx, provider, ChatRequest{})
		if err == nil {
			t.Fatal("CollectChat() error = nil")
		}
		if !strings.Contains(err.Error(), "openai-compatible chat completions request") {
			t.Fatalf("error missing request stage: %v", err)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled semantics", err)
		}
	})
}
```

Also add `errors` to the import list in `internal/llms/openai_compatible_test.go`:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/llms -run TestOpenAICompatibleProviderRequestErrorsKeepContextSemantics -count=1
```

Expected: FAIL because current request errors say `send openai chat request`.

- [ ] **Step 3: Implement request-stage wrapping**

In `doChatCompletions`, replace the `p.client.Do` error branch:

```go
resp, err := p.client.Do(httpReq)
if err != nil {
	return nil, fmt.Errorf("openai-compatible chat completions request: %w", err)
}
```

Leave `json.Marshal` and `http.NewRequestWithContext` messages unchanged unless tests prove they need the same provider prefix.

- [ ] **Step 4: Run request tests**

Run:

```bash
go test ./internal/llms -run TestOpenAICompatibleProviderRequestErrorsKeepContextSemantics -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/llms/openai_compatible.go internal/llms/openai_compatible_test.go
git commit -m "test: preserve openai request error semantics"
```

---

### Task 3: SSE Error Wording

**Files:**
- Modify: `internal/llms/openai_compatible_test.go`
- Modify: `internal/llms/openai_compatible.go`

- [ ] **Step 1: Strengthen existing SSE error tests**

In `TestOpenAICompatibleProviderChatStreamSurfacesStatusAndMalformedStreamErrors`, change the malformed chunk assertion to require the exact provider/stage prefix:

```go
if !strings.Contains(errorText, "parse openai-compatible stream chunk") {
	t.Fatalf("error missing parse context: %q", errorText)
}
```

Change the error frame assertion to require:

```go
if !strings.Contains(errorText, "openai-compatible stream error") {
	t.Fatalf("error missing stream error context: %q", errorText)
}
if !strings.Contains(errorText, "quota exhausted") {
	t.Fatalf("error missing quota message: %q", errorText)
}
```

In `TestOpenAICompatibleProviderChatStreamReturnsErrorWhenStreamEndsWithoutDone`, change the final assertion to:

```go
if !strings.Contains(errorText, "openai-compatible stream ended before completion") {
	t.Fatalf("error missing ended before completion: %#v", events)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/llms -run 'TestOpenAICompatibleProviderChatStream(SurfacesStatusAndMalformedStreamErrors|ReturnsErrorWhenStreamEndsWithoutDone|CompletesOnFinishReasonWithoutDone)' -count=1
```

Expected: FAIL because current stream errors use `openai chat stream` and `ended without done`.

- [ ] **Step 3: Implement stable SSE error wording**

In `ChatStream`, replace these branches:

```go
if err := ctx.Err(); err != nil {
	events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: fmt.Errorf("openai-compatible stream context: %w", err)}
	return
}
```

```go
if err != nil {
	if err == io.EOF {
		events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: fmt.Errorf("openai-compatible stream ended before completion")}
	} else {
		events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: fmt.Errorf("read openai-compatible stream: %w", err)}
	}
	return
}
```

```go
if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
	events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: fmt.Errorf("parse openai-compatible stream chunk: %w", err)}
	return
}
```

Replace `openAIStreamResponseError` with:

```go
func openAIStreamResponseError(streamErr *openAIStreamError) error {
	message := strings.TrimSpace(streamErr.Message)
	if message == "" {
		return fmt.Errorf("openai-compatible stream error")
	}
	return fmt.Errorf("openai-compatible stream error: %s", message)
}
```

- [ ] **Step 4: Run SSE tests**

Run:

```bash
go test ./internal/llms -run 'TestOpenAICompatibleProviderChatStream(SurfacesStatusAndMalformedStreamErrors|ReturnsErrorWhenStreamEndsWithoutDone|CompletesOnFinishReasonWithoutDone)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/llms/openai_compatible.go internal/llms/openai_compatible_test.go
git commit -m "test: stabilize openai stream errors"
```

---

### Task 4: Final Tool Call Validation

**Files:**
- Modify: `internal/llms/openai_compatible_test.go`
- Modify: `internal/llms/openai_compatible.go`

- [ ] **Step 1: Write failing final tool call validation tests**

Add this test near `TestOpenAICompatibleProviderChatStreamAggregatesToolCallChunks`:

```go
func TestOpenAICompatibleProviderChatStreamRejectsInvalidFinalToolCalls(t *testing.T) {
	tests := []struct {
		name        string
		payloads    []string
		wantMessage string
	}{
		{
			name: "invalid_arguments_json",
			payloads: []string{
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_bad","function":{"name":"calculator","arguments":"{bad json"}}]}}]}`,
				`{"choices":[{"finish_reason":"tool_calls"}]}`,
			},
			wantMessage: "openai-compatible tool call arguments are not valid JSON",
		},
		{
			name: "missing_id",
			payloads: []string{
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"calculator","arguments":"{\"a\":13}"}}]}}]}`,
				`{"choices":[{"finish_reason":"tool_calls"}]}`,
			},
			wantMessage: "openai-compatible tool call missing id",
		},
		{
			name: "missing_name",
			payloads: []string{
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_missing_name","function":{"arguments":"{\"a\":13}"}}]}}]}`,
				`{"choices":[{"finish_reason":"tool_calls"}]}`,
			},
			wantMessage: "openai-compatible tool call missing function name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeSSE(t, w, tt.payloads...)
			}))
			defer server.Close()

			provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 3})
			if err != nil {
				t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
			}

			stream, err := provider.ChatStream(context.Background(), ChatRequest{})
			if err != nil {
				t.Fatalf("ChatStream() error = %v", err)
			}
			events := collectChatStreamEvents(t, stream)

			var errorText string
			var sawDone bool
			for _, event := range events {
				switch event.Type {
				case ChatStreamEventTypeError:
					if event.Err == nil {
						t.Fatal("error event err = nil")
					}
					errorText = event.Err.Error()
				case ChatStreamEventTypeDone:
					sawDone = true
				}
			}
			if sawDone {
				t.Fatalf("unexpected done event: %#v", events)
			}
			if !strings.Contains(errorText, tt.wantMessage) {
				t.Fatalf("error = %q, want %q; events = %#v", errorText, tt.wantMessage, events)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internal/llms -run TestOpenAICompatibleProviderChatStreamRejectsInvalidFinalToolCalls -count=1
```

Expected: FAIL because current code emits a done event with invalid or incomplete final tool calls.

- [ ] **Step 3: Implement final tool call validation helper**

In `internal/llms/openai_compatible.go`, add this helper after `mergeOpenAIStreamToolCalls`:

```go
func validateOpenAIStreamToolCalls(toolCalls []openAIToolCall) error {
	for i, toolCall := range toolCalls {
		if strings.TrimSpace(toolCall.ID) == "" {
			return fmt.Errorf("openai-compatible tool call missing id at index %d", i)
		}
		if strings.TrimSpace(toolCall.Function.Name) == "" {
			return fmt.Errorf("openai-compatible tool call missing function name at index %d", i)
		}
		arguments := strings.TrimSpace(toolCall.Function.Arguments)
		if arguments == "" {
			continue
		}
		if !json.Valid([]byte(arguments)) {
			return fmt.Errorf("openai-compatible tool call arguments are not valid JSON at index %d", i)
		}
	}
	return nil
}
```

In `ChatStream`, before each done event, validate accumulated tool calls:

```go
if payload == "[DONE]" {
	if err := validateOpenAIStreamToolCalls(toolCalls); err != nil {
		events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: err}
		return
	}
	events <- ChatStreamEvent{Type: ChatStreamEventTypeDone, Message: openAIStreamMessage(role, content.String(), toolCalls)}
	return
}
```

And before the `finish_reason` done event:

```go
if choice.FinishReason != "" {
	if err := validateOpenAIStreamToolCalls(toolCalls); err != nil {
		events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: err}
		return
	}
	events <- ChatStreamEvent{Type: ChatStreamEventTypeDone, Message: openAIStreamMessage(role, content.String(), toolCalls)}
	return
}
```

Do not validate partial deltas before completion; partial argument JSON is expected to be invalid mid-stream.

- [ ] **Step 4: Run tool-call tests**

Run:

```bash
go test ./internal/llms -run 'TestOpenAICompatibleProvider(ChatStreamAggregatesToolCallChunks|NormalizesMissingToolCallTypeToFunction|ChatStreamRejectsInvalidFinalToolCalls)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/llms/openai_compatible.go internal/llms/openai_compatible_test.go
git commit -m "test: validate openai stream tool calls"
```

---

### Task 5: Full Provider Verification and Review Prep

**Files:**
- Modify only if previous focused tests reveal a real defect.

- [ ] **Step 1: Run focused provider suite**

Run:

```bash
go test ./internal/llms -count=1
```

Expected: PASS.

- [ ] **Step 2: Run adjacent package regression suite**

Run:

```bash
go test ./internal/application/service ./internal/application/router ./cmd/cli -count=1
```

Expected: PASS.

- [ ] **Step 3: Run vet**

Run:

```bash
go vet ./...
```

Expected: no output, exit 0.

- [ ] **Step 4: Commit any final cleanup**

Only if gofmt or tests required a final cleanup commit:

```bash
git add internal/llms/openai_compatible.go internal/llms/openai_compatible_test.go
git commit -m "test: complete openai provider hardening"
```

If there is no diff, skip this commit.

- [ ] **Step 5: Request code review**

Ask for review of the implementation commits. Required review scope:

```text
internal/llms/openai_compatible.go
internal/llms/openai_compatible_test.go
docs/superpowers/specs/2026-07-09-openai-provider-hardening-design.md
docs/superpowers/plans/2026-07-09-openai-provider-hardening.md
```

Review must check:

- No API key or Authorization header can appear in errors.
- Request timeout/cancel errors keep `errors.Is` semantics.
- SSE malformed/error/end cases produce stable errors.
- Final tool-call validation does not reject valid partial chunks before completion.
- Existing compatibility remains: missing type becomes `function`, finish reason without `[DONE]` completes, usage-only chunks are ignored.

---

## Final Verification

After all tasks and review fixes pass, run:

```bash
go test -count=1 ./...
go test -race -count=1 ./...
```

Expected:

```text
go test: all packages pass
race: all packages pass
```
