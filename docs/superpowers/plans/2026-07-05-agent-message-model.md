# Agent Message Model Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the approved Agent message model so Agent history uses typed semantic messages, legacy `llms.Message` input is validated before conversion, and tool execution errors enter history for model recovery under `MaxSteps`.

**Architecture:** Add `internal/agent.Message` and concrete `UserMessage`, `AssistantMessage`, and `ToolResultMessage` types. `RunAgentMessages` becomes the main Agent entry and keeps runtime history as `[]agent.Message`; each provider call converts that history to `[]llms.Message`. `RunMessages([]llms.Message)` becomes a compatibility layer that validates the wide DTO shape and converts prior tool results by matching `ToolCallID` to earlier assistant tool calls. Tool failures create `ToolResultMessage{IsError:true}` with sanitized content, record the original error in `RunResult.Step.Error`, and continue the loop.

**Tech Stack:** Go 1.26.4, standard `testing`, existing `internal/agent`, `internal/llms`, `internal/harness`, fake providers, table-driven tests.

---

## File Structure

- Create: `internal/agent/message.go`
  - Owns Agent-layer message types.
  - Owns conversion between `[]agent.Message` and `[]llms.Message`.
  - Validates illegal states that `llms.Message` can express.
- Modify: `internal/agent/loop.go`
  - Adds `RunAgentMessages` as the main loop.
  - Routes `Run`, `RunResult`, and `RunMessages` through the typed path.
  - Keeps `MaxSteps` as the only retry/loop guard.
- Modify: `internal/agent/tools.go`
  - Returns typed `ToolResultMessage` for success and failure.
  - Adds fixed-format safe tool error content.
- Modify: `internal/harness/harness.go`
  - Adds public `Harness.RunAgentMessages`.
  - Preserves harness-level `empty input` error behavior.
- Modify: `internal/agent/loop_test.go`
  - Adds tests for semantic history conversion, invalid typed messages, illegal wide DTOs, tool-error recovery, and `MaxSteps` after tool errors.
  - Rewrites the old fail-fast tool-error test.
- Modify: `internal/harness/harness_test.go`
  - Adds black-box tests for the new harness API.

---

### Task 1: Add Agent message model and conversion

**Files:**
- Create: `internal/agent/message.go`
- Modify: `internal/agent/loop_test.go`

- [ ] **Step 1: Write failing typed-history and validation tests**

Add a helper near the existing `runMessages` helper in `internal/agent/loop_test.go`:

```go
type runAgentMessagesRunner interface {
	RunAgentMessages(context.Context, []Message) (*RunResult, error)
}

func runAgentMessages(t *testing.T, a *Agent, ctx context.Context, messages []Message) (*RunResult, error) {
	t.Helper()

	runner, ok := any(a).(runAgentMessagesRunner)
	if !ok {
		t.Fatal("*Agent does not implement RunAgentMessages(context.Context, []Message) (*RunResult, error)")
	}
	return runner.RunAgentMessages(ctx, messages)
}
```

Append these tests near the existing history tests:

```go
func TestAgentRunAgentMessagesForwardsSemanticHistoryToProvider(t *testing.T) {
	history := []Message{
		UserMessage{Content: "what is 2 + 2?"},
		AssistantMessage{ToolCalls: []llms.ToolCall{
			calculatorToolCall("call_1", `{"a":2,"b":2,"op":"add"}`),
		}},
		ToolResultMessage{ToolCallID: "call_1", ToolName: "calculator", Content: "4"},
		AssistantMessage{Content: "2 + 2 = 4."},
		UserMessage{Content: "multiply that by 3"},
	}

	want := []llms.Message{
		{Role: llms.RoleUser, Content: "what is 2 + 2?"},
		{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{
			calculatorToolCall("call_1", `{"a":2,"b":2,"op":"add"}`),
		}},
		{Role: llms.RoleTool, ToolCallID: "call_1", Content: "4"},
		{Role: llms.RoleAssistant, Content: "2 + 2 = 4."},
		{Role: llms.RoleUser, Content: "multiply that by 3"},
	}

	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		assertMessagesEqual(t, req.Messages, want)
		return &llms.ChatResponse{Message: llms.Message{
			Role:    llms.RoleAssistant,
			Content: "4 * 3 = 12",
		}}, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := runAgentMessages(t, a, context.Background(), history)
	if err != nil {
		t.Fatalf("RunAgentMessages() error = %v", err)
	}
	if got == nil || got.Answer != "4 * 3 = 12" {
		t.Fatalf("RunAgentMessages().Answer = %#v, want %q", got, "4 * 3 = 12")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests len = %d, want 1", len(provider.requests))
	}
}

func TestAgentRunAgentMessagesRejectsInvalidHistoryAndSemanticMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
	}{
		{name: "nil history", messages: nil},
		{name: "empty history", messages: []Message{}},
		{name: "last message not user", messages: []Message{
			AssistantMessage{Content: "hi"},
		}},
		{name: "last user message whitespace", messages: []Message{
			UserMessage{Content: " \n\t "},
		}},
		{name: "earlier user empty", messages: []Message{
			UserMessage{Content: "   "},
			UserMessage{Content: "continue"},
		}},
		{name: "assistant empty", messages: []Message{
			AssistantMessage{},
			UserMessage{Content: "continue"},
		}},
		{name: "tool result missing tool call id", messages: []Message{
			ToolResultMessage{ToolName: "calculator", Content: "3"},
			UserMessage{Content: "continue"},
		}},
		{name: "tool result missing tool name", messages: []Message{
			ToolResultMessage{ToolCallID: "call_1", Content: "3"},
			UserMessage{Content: "continue"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
				t.Fatal("provider.Chat() should not be called for invalid semantic history")
				return nil, nil
			})}

			a := New(provider, tools.NewRegistry(), "fake-tool-model")
			got, err := runAgentMessages(t, a, context.Background(), tt.messages)
			if err == nil {
				t.Fatalf("RunAgentMessages() error = nil, result = %#v", got)
			}
			if len(provider.requests) != 0 {
				t.Fatalf("provider requests len = %d, want 0", len(provider.requests))
			}
		})
	}
}
```

- [ ] **Step 2: Write failing legacy wide-message tests**

Append these tests near existing `RunMessages` history tests:

```go
func TestAgentRunMessagesForwardsWideHistoryWithPriorToolResult(t *testing.T) {
	history := []llms.Message{
		{Role: llms.RoleUser, Content: "what is 2 + 2?"},
		{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{
			calculatorToolCall("call_1", `{"a":2,"b":2,"op":"add"}`),
		}},
		{Role: llms.RoleTool, ToolCallID: "call_1", Content: "4"},
		{Role: llms.RoleAssistant, Content: "2 + 2 = 4."},
		{Role: llms.RoleUser, Content: "multiply that by 3"},
	}

	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		assertMessagesEqual(t, req.Messages, history)
		return &llms.ChatResponse{Message: llms.Message{
			Role:    llms.RoleAssistant,
			Content: "4 * 3 = 12",
		}}, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := runMessages(t, a, context.Background(), history)
	if err != nil {
		t.Fatalf("RunMessages() error = %v", err)
	}
	if got == nil || got.Answer != "4 * 3 = 12" {
		t.Fatalf("RunMessages().Answer = %#v, want %q", got, "4 * 3 = 12")
	}
}

func TestAgentRunMessagesRejectsIllegalWideMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []llms.Message
	}{
		{
			name: "user with tool calls",
			messages: []llms.Message{
				{Role: llms.RoleUser, Content: "hello", ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_1", `{"a":1,"b":2,"op":"add"}`),
				}},
				{Role: llms.RoleUser, Content: "continue"},
			},
		},
		{
			name: "assistant with tool call id",
			messages: []llms.Message{
				{Role: llms.RoleAssistant, Content: "hi", ToolCallID: "call_1"},
				{Role: llms.RoleUser, Content: "continue"},
			},
		},
		{
			name: "assistant empty",
			messages: []llms.Message{
				{Role: llms.RoleAssistant},
				{Role: llms.RoleUser, Content: "continue"},
			},
		},
		{
			name: "tool result missing tool call id",
			messages: []llms.Message{
				{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_1", `{"a":1,"b":2,"op":"add"}`),
				}},
				{Role: llms.RoleTool, Content: "3"},
				{Role: llms.RoleUser, Content: "continue"},
			},
		},
		{
			name: "tool result without matching assistant tool call",
			messages: []llms.Message{
				{Role: llms.RoleTool, ToolCallID: "missing", Content: "3"},
				{Role: llms.RoleUser, Content: "continue"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
				t.Fatal("provider.Chat() should not be called for illegal llms.Message history")
				return nil, nil
			})}
			a := New(provider, tools.NewRegistry(), "fake-tool-model")

			got, err := runMessages(t, a, context.Background(), tt.messages)
			if err == nil {
				t.Fatalf("RunMessages() error = nil, result = %#v", got)
			}
			if len(provider.requests) != 0 {
				t.Fatalf("provider requests len = %d, want 0", len(provider.requests))
			}
		})
	}
}
```

- [ ] **Step 3: Run focused tests to verify RED**

Run:

```bash
go test ./internal/agent -run 'TestAgentRunAgentMessages|TestAgentRunMessagesForwardsWideHistoryWithPriorToolResult|TestAgentRunMessagesRejectsIllegalWideMessages'
```

Expected: FAIL at compile stage with `undefined: Message`, `undefined: UserMessage`, and `RunAgentMessages` missing.

- [ ] **Step 4: Implement message types and conversion helpers**

Create `internal/agent/message.go`:

```go
package agent

import (
	"fmt"
	"strings"

	"harukizmoe/pimoe/internal/llms"
)

// Message 表示 Agent 内部使用的强语义对话消息。
type Message interface {
	agentMessage()
}

// UserMessage 表示用户输入消息。
type UserMessage struct {
	// Content 是发送给模型的用户文本；转换前会裁剪首尾空白。
	Content string
}

func (UserMessage) agentMessage() {}

// AssistantMessage 表示模型响应消息，可包含最终文本或 tool calls。
type AssistantMessage struct {
	// Content 是模型返回的可见文本。
	Content string
	// ToolCalls 是模型请求执行的本地工具调用。
	ToolCalls []llms.ToolCall
}

func (AssistantMessage) agentMessage() {}

// ToolResultMessage 表示一次本地工具执行后返回给模型的结果。
type ToolResultMessage struct {
	// ToolCallID 关联模型发起的 assistant tool call。
	ToolCallID string
	// ToolName 是被执行的本地工具名，用于 trace、日志和错误摘要。
	ToolName string
	// Content 是发送给模型的工具结果文本；失败时必须是安全错误摘要。
	Content string
	// IsError 标记该工具结果是否表示执行失败。
	IsError bool
}

func (ToolResultMessage) agentMessage() {}

func toLLMMessages(messages []Message) ([]llms.Message, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}

	out := make([]llms.Message, 0, len(messages))
	for i, message := range messages {
		converted, err := toLLMMessage(message)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		out = append(out, converted)
	}
	return out, nil
}

func toLLMMessage(message Message) (llms.Message, error) {
	switch msg := message.(type) {
	case UserMessage:
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			return llms.Message{}, fmt.Errorf("user message content must not be empty")
		}
		return llms.Message{Role: llms.RoleUser, Content: content}, nil
	case AssistantMessage:
		toolCalls := append([]llms.ToolCall(nil), msg.ToolCalls...)
		if strings.TrimSpace(msg.Content) == "" && len(toolCalls) == 0 {
			return llms.Message{}, fmt.Errorf("assistant message must have content or tool calls")
		}
		return llms.Message{Role: llms.RoleAssistant, Content: msg.Content, ToolCalls: toolCalls}, nil
	case ToolResultMessage:
		if strings.TrimSpace(msg.ToolCallID) == "" {
			return llms.Message{}, fmt.Errorf("tool result message must have tool call id")
		}
		if strings.TrimSpace(msg.ToolName) == "" {
			return llms.Message{}, fmt.Errorf("tool result message must have tool name")
		}
		return llms.Message{Role: llms.RoleTool, ToolCallID: msg.ToolCallID, Content: msg.Content}, nil
	default:
		return llms.Message{}, fmt.Errorf("unsupported agent message type %T", message)
	}
}

func fromLLMMessages(messages []llms.Message) ([]Message, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}

	toolNamesByID := make(map[string]string)
	out := make([]Message, 0, len(messages))
	for i, message := range messages {
		converted, err := fromLLMMessage(message, toolNamesByID)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		out = append(out, converted)
	}
	return out, nil
}

func fromLLMMessage(message llms.Message, toolNamesByID map[string]string) (Message, error) {
	switch message.Role {
	case llms.RoleUser:
		if len(message.ToolCalls) > 0 || message.ToolCallID != "" {
			return nil, fmt.Errorf("user message cannot have tool calls or tool call id")
		}
		return UserMessage{Content: message.Content}, nil
	case llms.RoleAssistant:
		if message.ToolCallID != "" {
			return nil, fmt.Errorf("assistant message cannot have tool call id")
		}
		toolCalls := append([]llms.ToolCall(nil), message.ToolCalls...)
		if strings.TrimSpace(message.Content) == "" && len(toolCalls) == 0 {
			return nil, fmt.Errorf("assistant message must have content or tool calls")
		}
		for _, call := range toolCalls {
			if call.ID == "" {
				return nil, fmt.Errorf("assistant tool call must have id")
			}
			if call.Function.Name == "" {
				return nil, fmt.Errorf("assistant tool call must have function name")
			}
			toolNamesByID[call.ID] = call.Function.Name
		}
		return AssistantMessage{Content: message.Content, ToolCalls: toolCalls}, nil
	case llms.RoleTool:
		if len(message.ToolCalls) > 0 {
			return nil, fmt.Errorf("tool message cannot have tool calls")
		}
		if message.ToolCallID == "" {
			return nil, fmt.Errorf("tool message must have tool call id")
		}
		toolName, ok := toolNamesByID[message.ToolCallID]
		if !ok {
			return nil, fmt.Errorf("tool message must match a previous assistant tool call")
		}
		return ToolResultMessage{ToolCallID: message.ToolCallID, ToolName: toolName, Content: message.Content}, nil
	default:
		return nil, fmt.Errorf("unsupported message role %q", message.Role)
	}
}
```

- [ ] **Step 5: Run focused tests after conversion implementation**

Run:

```bash
gofmt -w internal/agent/message.go internal/agent/loop_test.go
go test ./internal/agent -run 'TestAgentRunMessagesForwardsWideHistoryWithPriorToolResult|TestAgentRunMessagesRejectsIllegalWideMessages|TestAgentRunAgentMessagesForwardsSemanticHistoryToProvider|TestAgentRunAgentMessagesRejectsInvalidHistoryAndSemanticMessages'
```

Expected: still FAIL. At this point the new types compile, but `RunMessages` is not yet routed through `fromLLMMessages` and `RunAgentMessages` is not yet implemented. This confirms the tests are guarding the next task.

- [ ] **Step 6: Do not commit yet**

Leave Task 1 changes uncommitted. Task 2 is the smallest unit that turns these RED tests GREEN and produces the first commit.

---

### Task 2: Route Agent loop through typed messages

**Files:**
- Modify: `internal/agent/loop.go`
- Modify: `internal/agent/loop_test.go`

- [ ] **Step 1: Run typed-entry focused tests to verify RED**

Run:

```bash
go test ./internal/agent -run 'TestAgentRunAgentMessagesForwardsSemanticHistoryToProvider|TestAgentRunAgentMessagesRejectsInvalidHistoryAndSemanticMessages'
```

Expected: FAIL with `RunAgentMessages` missing.

- [ ] **Step 2: Write failing invalid assistant response test**

Append near final-answer tests in `internal/agent/loop_test.go`:

```go
func TestAgentRunAgentMessagesRejectsEmptyAssistantResponse(t *testing.T) {
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		return &llms.ChatResponse{Message: llms.Message{Role: llms.RoleAssistant}}, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := runAgentMessages(t, a, context.Background(), []Message{
		UserMessage{Content: "answer directly"},
	})
	if err == nil {
		t.Fatalf("RunAgentMessages() error = nil, result = %#v", got)
	}
	if !strings.Contains(err.Error(), "assistant message must have content or tool calls") {
		t.Fatalf("RunAgentMessages() error = %q, want assistant validation", err.Error())
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests len = %d, want 1", len(provider.requests))
	}
}
```

- [ ] **Step 3: Run invalid assistant response test to verify RED**

Run:

```bash
go test ./internal/agent -run TestAgentRunAgentMessagesRejectsEmptyAssistantResponse
```

Expected: FAIL with `RunAgentMessages` still missing. After Step 4 initially adds `RunAgentMessages`, this test must fail until the assistant-response validation line is present.

- [ ] **Step 4: Implement `RunAgentMessages` as the main loop**

Update `internal/agent/loop.go` so the public entrypoints are:

```go
// Run 执行一次有步数上限的 tool calling 主循环，并返回最终回答。
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	result, err := a.RunResult(ctx, input)
	if err != nil {
		return "", err
	}
	return result.Answer, nil
}

// RunResult 执行一次有步数上限的 tool calling 主循环，并返回结构化 trace。
func (a *Agent) RunResult(ctx context.Context, input string) (*RunResult, error) {
	return a.RunAgentMessages(ctx, []Message{UserMessage{Content: input}})
}

// RunMessages 从调用方提供的无状态 LLM DTO 历史继续执行 tool calling 主循环。
func (a *Agent) RunMessages(ctx context.Context, messages []llms.Message) (*RunResult, error) {
	agentMessages, err := fromLLMMessages(messages)
	if err != nil {
		return nil, err
	}
	return a.RunAgentMessages(ctx, agentMessages)
}

// RunAgentMessages 从调用方提供的强语义无状态对话历史继续执行 tool calling 主循环。
func (a *Agent) RunAgentMessages(ctx context.Context, messages []Message) (*RunResult, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}
	lastMessage, ok := messages[len(messages)-1].(UserMessage)
	if !ok || strings.TrimSpace(lastMessage.Content) == "" {
		return nil, fmt.Errorf("last message must be a non-empty user message")
	}

	messages = append([]Message(nil), messages...)
	result := &RunResult{}
	toolSchemas := a.tools.Schemas()

	a.logger.Info(ctx, "agent.run.start", "model", a.model, "input", strings.TrimSpace(lastMessage.Content))
	for chatRound := 0; ; chatRound++ {
		llmMessages, err := toLLMMessages(messages)
		if err != nil {
			return result, err
		}
		a.logLLMRequest(ctx, chatRound, len(llmMessages), len(toolSchemas))
		response, err := a.provider.Chat(ctx, llms.ChatRequest{
			Model:    a.model,
			Messages: llmMessages,
			Tools:    toolSchemas,
		})
		if err != nil {
			a.logLLMError(ctx, chatRound, err)
			return result, fmt.Errorf("llm chat round %d: %w", chatRound+1, err)
		}

		assistantMessage := AssistantMessage{
			Content:   response.Message.Content,
			ToolCalls: append([]llms.ToolCall(nil), response.Message.ToolCalls...),
		}
		if _, err := toLLMMessage(assistantMessage); err != nil {
			return result, fmt.Errorf("assistant response: %w", err)
		}
		if len(assistantMessage.ToolCalls) == 0 {
			result.Answer = assistantMessage.Content
			a.logger.Info(ctx, "agent.run.done", "answer", assistantMessage.Content)
			a.emit(Event{Type: EventFinal, Message: assistantMessage.Content})
			return result, nil
		}

		if result.ToolRounds >= a.maxSteps {
			a.logger.Error(ctx, "agent.max_steps.exceeded", "max_steps", a.maxSteps, "tool_calls", len(assistantMessage.ToolCalls))
			return result, fmt.Errorf("agent max steps exceeded after %d tool-calling rounds", a.maxSteps)
		}

		a.logger.Info(ctx, "agent.tool_calls.received", "count", len(assistantMessage.ToolCalls))
		messages = append(messages, assistantMessage)
		for _, call := range assistantMessage.ToolCalls {
			a.logger.Debug(ctx, "agent.tool.call", "name", call.Function.Name, "arguments", call.Function.Arguments)
			a.emit(Event{Type: EventToolCall, Message: call.Function.Name})

			step := Step{ToolCallID: call.ID, ToolName: call.Function.Name, Arguments: call.Function.Arguments}
			toolMessage, err := a.runToolCall(ctx, call)
			if err != nil {
				step.Error = err.Error()
				a.logger.Error(ctx, "agent.tool.error", "name", call.Function.Name, "error", err)
			} else {
				step.Result = toolMessage.Content
				a.logger.Debug(ctx, "agent.tool.result", "name", call.Function.Name, "content", toolMessage.Content)
			}
			result.Steps = append(result.Steps, step)
			a.emit(Event{Type: EventToolResult, Message: toolMessage.Content})
			messages = append(messages, toolMessage)
		}
		result.ToolRounds++
	}
}
```

Keep `logLLMRequest`, `logLLMError`, and `emit` unchanged below the loop.

- [ ] **Step 5: Run focused conversion and route tests**

Run:

```bash
gofmt -w internal/agent/message.go internal/agent/loop.go internal/agent/loop_test.go
go test ./internal/agent -run 'TestAgentRunAgentMessagesForwardsSemanticHistoryToProvider|TestAgentRunAgentMessagesRejectsInvalidHistoryAndSemanticMessages|TestAgentRunAgentMessagesRejectsEmptyAssistantResponse|TestAgentRunMessagesForwardsWideHistoryWithPriorToolResult|TestAgentRunMessagesRejectsIllegalWideMessages|TestAgentRunMessagesForwardsProvidedHistoryToProvider|TestAgentRunMessagesRejectsInvalidHistory'
```

Expected: PASS, except existing tests may fail if they still expect unsupported old illegal states. Fix only tests that conflict with the approved spec.

- [ ] **Step 6: Commit typed routing**

Run:

```bash
git add internal/agent/message.go internal/agent/loop.go internal/agent/loop_test.go
git commit -m "feat: route agent loop through typed messages"
```

---

### Task 3: Send tool errors back to the model

**Files:**
- Modify: `internal/agent/tools.go`
- Modify: `internal/agent/loop_test.go`

- [ ] **Step 1: Rewrite old fail-fast test as recovery behavior**

Replace `TestAgentRunResultReturnsTraceWhenToolFails` with:

```go
func TestAgentRunAgentMessagesContinuesAfterToolErrorAndReturnsFinalAnswer(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		round++
		switch round {
		case 1:
			return &llms.ChatResponse{Message: llms.Message{
				Role: llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_bad_args", `{"a":1`),
				},
			}}, nil
		case 2:
			got := req.Messages[len(req.Messages)-1]
			if got.Role != llms.RoleTool {
				t.Fatalf("second request last role = %q, want tool", got.Role)
			}
			if got.ToolCallID != "call_bad_args" {
				t.Fatalf("second request ToolCallID = %q, want call_bad_args", got.ToolCallID)
			}
			if !strings.HasPrefix(got.Content, `tool "calculator" failed: `) {
				t.Fatalf("second request tool content = %q, want sanitized failure summary", got.Content)
			}
			if !strings.Contains(got.Content, "decode calculator arguments") {
				t.Fatalf("second request tool content = %q, want calculator decode context", got.Content)
			}
			return &llms.ChatResponse{Message: llms.Message{
				Role:    llms.RoleAssistant,
				Content: "I couldn't use calculator because the arguments were malformed.",
			}}, nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := New(provider, registry, "fake-tool-model")
	got, err := runAgentMessages(t, a, context.Background(), []Message{
		UserMessage{Content: "try calculator with bad arguments"},
	})
	if err != nil {
		t.Fatalf("RunAgentMessages() error = %v", err)
	}
	if got == nil {
		t.Fatal("RunAgentMessages() result = nil")
	}
	if got.Answer != "I couldn't use calculator because the arguments were malformed." {
		t.Fatalf("RunAgentMessages().Answer = %q", got.Answer)
	}
	if got.ToolRounds != 1 {
		t.Fatalf("RunAgentMessages().ToolRounds = %d, want 1", got.ToolRounds)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("RunAgentMessages().Steps len = %d, want 1", len(got.Steps))
	}
	step := got.Steps[0]
	if step.Result != "" {
		t.Fatalf("RunAgentMessages().Steps[0].Result = %q, want empty", step.Result)
	}
	if step.Error == "" || !strings.Contains(step.Error, "decode calculator arguments") {
		t.Fatalf("RunAgentMessages().Steps[0].Error = %q, want calculator decode failure", step.Error)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider requests len = %d, want 2", len(provider.requests))
	}
}
```

- [ ] **Step 2: Add `MaxSteps` guard after tool-error retry**

Append:

```go
func TestAgentRunAgentMessagesReturnsMaxStepsErrorWhenModelKeepsRetryingAfterToolError(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		round++
		switch round {
		case 1:
			return &llms.ChatResponse{Message: llms.Message{
				Role: llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_bad_args", `{"a":1`),
				},
			}}, nil
		case 2:
			got := req.Messages[len(req.Messages)-1]
			if got.Role != llms.RoleTool || !strings.HasPrefix(got.Content, `tool "calculator" failed: `) {
				t.Fatalf("second request last message = %#v, want sanitized tool failure", got)
			}
			return &llms.ChatResponse{Message: llms.Message{
				Role: llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_retry", `{"a":1`),
				},
			}}, nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := NewWithOptions(provider, registry, "fake-tool-model", Options{MaxSteps: 1})
	got, err := runAgentMessages(t, a, context.Background(), []Message{
		UserMessage{Content: "keep trying even after tool failure"},
	})
	if err == nil {
		t.Fatal("RunAgentMessages() error = nil, want max steps error")
	}
	if !strings.Contains(err.Error(), "max steps") {
		t.Fatalf("RunAgentMessages() error = %v, want max steps message", err)
	}
	if got == nil {
		t.Fatal("RunAgentMessages() result = nil, want retained step trace")
	}
	if got.ToolRounds != 1 {
		t.Fatalf("RunAgentMessages().ToolRounds = %d, want 1", got.ToolRounds)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("RunAgentMessages().Steps len = %d, want 1", len(got.Steps))
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider requests len = %d, want 2", len(provider.requests))
	}
}
```

- [ ] **Step 3: Run tool-error tests to verify RED**

Run:

```bash
go test ./internal/agent -run 'TestAgentRunAgentMessagesContinuesAfterToolErrorAndReturnsFinalAnswer|TestAgentRunAgentMessagesReturnsMaxStepsErrorWhenModelKeepsRetryingAfterToolError'
```

Expected before `tools.go` change: FAIL because `runToolCall` still returns `llms.Message` and/or tool errors are not represented as typed error results.

- [ ] **Step 4: Return typed tool results for success and failure**

Replace `internal/agent/tools.go` with:

```go
package agent

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

// runToolCall 负责把一次模型 tool call 转发给本地工具注册表，并封装回 Agent tool result 消息。
func (a *Agent) runToolCall(ctx context.Context, call llms.ToolCall) (ToolResultMessage, error) {
	result, err := a.tools.Call(ctx, call.Function.Name, call.Function.Arguments)
	if err != nil {
		return ToolResultMessage{
			ToolCallID: call.ID,
			ToolName:   call.Function.Name,
			Content:    safeToolErrorContent(call.Function.Name, err),
			IsError:    true,
		}, fmt.Errorf("call tool %q: %w", call.Function.Name, err)
	}

	return ToolResultMessage{
		ToolCallID: call.ID,
		ToolName:   call.Function.Name,
		Content:    result,
	}, nil
}

func safeToolErrorContent(toolName string, err error) string {
	return fmt.Sprintf("tool %q failed: %v", toolName, err)
}
```

- [ ] **Step 5: Run focused Agent tests**

Run:

```bash
gofmt -w internal/agent/tools.go internal/agent/loop.go internal/agent/loop_test.go
go test ./internal/agent -run 'TestAgentRunAgentMessagesContinuesAfterToolErrorAndReturnsFinalAnswer|TestAgentRunAgentMessagesReturnsMaxStepsErrorWhenModelKeepsRetryingAfterToolError|TestAgentRunExecutesToolCall|TestAgentRunResultRecordsToolTraceAcrossRounds|TestAgentRunReturnsMaxStepsErrorWhenToolLoopExceedsLimit'
```

Expected: PASS.

- [ ] **Step 6: Commit tool-error recovery**

Run:

```bash
git add internal/agent/tools.go internal/agent/loop.go internal/agent/loop_test.go
git commit -m "feat: let model recover from tool errors"
```

---

### Task 4: Add Harness typed-message API

**Files:**
- Modify: `internal/harness/harness.go`
- Modify: `internal/harness/harness_test.go`

- [ ] **Step 1: Write failing harness API tests**

Append before `newFakeHarness` in `internal/harness/harness_test.go`:

```go
func TestRunAgentMessagesRejectsEmptyFinalUserMessageAfterTrim(t *testing.T) {
	h := newFakeHarness(t)

	tests := []struct {
		name     string
		messages []agent.Message
	}{
		{
			name: "whitespace only user",
			messages: []agent.Message{
				agent.UserMessage{Content: " \n\t "},
			},
		},
		{
			name: "history ending with whitespace user",
			messages: []agent.Message{
				agent.AssistantMessage{Content: "Need anything else?"},
				agent.UserMessage{Content: "   \t"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.RunAgentMessages(context.Background(), tt.messages)
			if err == nil {
				t.Fatal("RunAgentMessages() error = nil, want empty input error")
			}
			if !strings.Contains(err.Error(), "empty input") {
				t.Fatalf("RunAgentMessages() error = %v, want empty input message", err)
			}
		})
	}
}

func TestRunAgentMessagesContinuesHistoryEndingWithUserMessage(t *testing.T) {
	h := newFakeHarness(t)

	got, err := h.RunAgentMessages(context.Background(), []agent.Message{
		agent.AssistantMessage{Content: "Let's continue from the earlier steps."},
		agent.UserMessage{Content: "  use calculator to compute 13 * 7  "},
	})
	if err != nil {
		t.Fatalf("RunAgentMessages() error = %v", err)
	}
	if got == nil {
		t.Fatal("RunAgentMessages() result = nil")
	}
	if got.Answer != "13 * 7 = 91" {
		t.Fatalf("RunAgentMessages().Answer = %q, want %q", got.Answer, "13 * 7 = 91")
	}
}
```

- [ ] **Step 2: Run harness tests to verify RED**

Run:

```bash
go test ./internal/harness -run 'TestRunAgentMessagesRejectsEmptyFinalUserMessageAfterTrim|TestRunAgentMessagesContinuesHistoryEndingWithUserMessage'
```

Expected: FAIL with `h.RunAgentMessages undefined`.

- [ ] **Step 3: Add `Harness.RunAgentMessages`**

Modify `internal/harness/harness.go`:

```go
// Run 执行一次 Agent 运行，并返回结构化结果。
func (h *Harness) Run(ctx context.Context, input string) (*agent.RunResult, error) {
	return h.RunAgentMessages(ctx, []agent.Message{agent.UserMessage{Content: input}})
}

// RunAgentMessages 从调用方提供的强语义无状态对话历史继续执行 Agent。
func (h *Harness) RunAgentMessages(ctx context.Context, messages []agent.Message) (*agent.RunResult, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("empty input")
	}

	lastMessage, ok := messages[len(messages)-1].(agent.UserMessage)
	if ok && strings.TrimSpace(lastMessage.Content) == "" {
		return nil, fmt.Errorf("empty input")
	}

	return h.agent.RunAgentMessages(ctx, messages)
}

// RunMessages 从调用方提供的无状态 LLM DTO 历史继续执行 Agent。
func (h *Harness) RunMessages(ctx context.Context, messages []llms.Message) (*agent.RunResult, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("empty input")
	}

	messages = append([]llms.Message(nil), messages...)
	lastIndex := len(messages) - 1
	messages[lastIndex].Content = strings.TrimSpace(messages[lastIndex].Content)
	if messages[lastIndex].Role == llms.RoleUser && messages[lastIndex].Content == "" {
		return nil, fmt.Errorf("empty input")
	}

	return h.agent.RunMessages(ctx, messages)
}
```

Keep existing imports; `fmt`, `strings`, `agent`, and `llms` remain used.

- [ ] **Step 4: Run harness-focused tests**

Run:

```bash
gofmt -w internal/harness/harness.go internal/harness/harness_test.go
go test ./internal/harness -run 'TestRunAgentMessagesRejectsEmptyFinalUserMessageAfterTrim|TestRunAgentMessagesContinuesHistoryEndingWithUserMessage|TestRunMessagesContinuesHistoryEndingWithUserMessage|TestRunMatchesRunMessagesForSingleUserInput'
```

Expected: PASS.

- [ ] **Step 5: Commit harness API**

Run:

```bash
git add internal/harness/harness.go internal/harness/harness_test.go
git commit -m "feat: expose typed harness messages"
```

---

### Task 5: Final verification and cleanup

**Files:**
- Modify only touched implementation/test files if verification exposes issues.

- [ ] **Step 1: Run changed package tests**

Run:

```bash
go test ./internal/agent ./internal/harness
```

Expected: PASS.

- [ ] **Step 2: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run vet**

Run:

```bash
go vet ./...
```

Expected: no output and exit code 0.

- [ ] **Step 4: Check golangci-lint availability**

Run:

```bash
command -v golangci-lint
```

Expected in the current workstation: non-zero exit because `golangci-lint` is not installed. If it prints a path, run:

```bash
golangci-lint run ./...
```

Expected when installed: PASS with no issues.

- [ ] **Step 5: Inspect diff for obsolete fail-fast assumptions**

Run:

```bash
git diff -- internal/agent internal/harness
```

Expected:

- No code path returns directly from local tool execution failure.
- No test expects calculator decode failure to make `RunResult` return `err` immediately.
- `RunMessages([]llms.Message)` rejects illegal wide states before provider call.
- `RunAgentMessages` is the main Agent path.

- [ ] **Step 6: Final commit only if verification fixes changed files**

If Steps 1-5 required fixes, run:

```bash
git add internal/agent internal/harness
git commit -m "test: verify agent message model"
```

If no files changed after prior commits, do not create an empty commit.

---

## Do Not Add

- No `MaxToolRetries`; failed and successful tool rounds both count against `MaxSteps`.
- No Agent auto-replay of the same failed tool call.
- No session, memory, compaction, image blocks, thinking blocks, or UI custom messages.
- No `IsError` field on `llms.Message`; error state remains Agent-layer only.
- No wrapper-equivalence tests that only prove one public method calls another.

## Verification Summary Required Before Completion

The implementation is complete only after reporting exact observed outputs for:

```bash
go test ./internal/agent ./internal/harness
go test ./...
go vet ./...
command -v golangci-lint
```

If `golangci-lint` is installed, also report:

```bash
golangci-lint run ./...
```
