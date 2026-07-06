# Event-first Harness Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `RunResult`-centered agent/harness execution with an event-first, stateful harness session that persists transcript only at terminal message events.

**Architecture:** `internal/agent` becomes a stateless event stream producer: it accepts caller-owned `[]agent.Message`, runs one turn, and emits lifecycle events. `internal/harness.Session` owns transcript, active-turn state, cancellation, and observer events. `cmd/cli` derives answer and trace from events with a local collector instead of calling `RunResult` APIs.

**Tech Stack:** Go 1.26.4, standard library channels/context/sync, existing `internal/llms`, `internal/tools`, `internal/logger`, fake provider tests.

---

## File Structure

- Modify: `internal/agent/events.go`
  - Replace request/tool/final events with message lifecycle events.
  - Keep `Event` sealed by `AgentEvent()`.
- Modify: `internal/agent/loop.go`
  - Rename public stream API to `Stream(ctx, []Message) <-chan Event`.
  - Remove `RunAgentMessages`, `StreamAgentMessages`, and `collectRunResult` from production code.
  - Emit `RunStart`, `TurnStart`, `MessageStart`, `MessageDelta`, `MessageEnd`, `ToolExecutionStart`, `ToolExecutionEnd`, `TurnEnd`, `RunEnd`, `Error`.
- Modify: `internal/agent/type.go`
  - Delete `RunResult` and `Step`.
  - Keep `RunRequest` / `RunResponse` only if still used by an existing entry point; otherwise delete them during cleanup.
- Modify: `internal/agent/loop_api_surface_regression_test.go`
  - Lock the stream-only public API.
- Create: `internal/agent/event_lifecycle_test.go`
  - RED tests for no-tool and tool-call lifecycle event sequences.
- Create: `internal/harness/session.go`
  - Add `Harness.NewSession`, `Session.Prompt`, `Session.Events`, `Session.Messages`, `Session.Cancel`.
  - Own transcript and active-turn state.
- Modify: `internal/harness/events.go`
  - Alias new agent event types only.
- Modify: `internal/harness/harness.go`
  - Keep config assembly in `New`.
  - Remove old `Run`, `RunAgentMessages`, and `Stream` execution helpers after tests move to `Session`.
- Create: `internal/harness/session_test.go`
  - RED tests for transcript persistence, snapshot copying, empty input, and concurrent prompt rejection.
- Modify: `internal/harness/harness_test.go`
  - Update config tests to run through `NewSession().Prompt` and collect terminal events.
- Modify: `internal/harness/stream_contract_test.go`
  - Replace old `Stream` contract tests with session event lifecycle tests or delete duplicated coverage once `session_test.go` owns it.
- Modify: `cmd/cli/main.go`
  - Use `runner.NewSession().Prompt`.
  - Add local event collector and formatter.
- Modify: `cmd/cli/main_test.go`
  - Replace `formatRunResult` tests with event collector/formatter tests.

---

### Task 1: Lock the agent API and lifecycle test contract

**Files:**
- Modify: `internal/agent/loop_api_surface_regression_test.go`
- Create: `internal/agent/event_lifecycle_test.go`

- [ ] **Step 1: Replace the API surface regression test**

Replace `internal/agent/loop_api_surface_regression_test.go` with:

```go
package agent

import (
	"context"
	"reflect"
	"testing"
)

func TestAgentLoopExecutionAPIIsEventStreamOnly(t *testing.T) {
	agentType := reflect.TypeOf((*Agent)(nil))
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	agentMessagesType := reflect.TypeOf([]Message(nil))
	eventStreamType := reflect.TypeOf((<-chan Event)(nil))

	assertRequiredLoopMethodSignature(t, agentType, "Stream", []reflect.Type{ctxType, agentMessagesType}, []reflect.Type{eventStreamType})

	for _, forbidden := range []string{"RunAgentMessages", "StreamAgentMessages"} {
		if method, ok := agentType.MethodByName(forbidden); ok {
			t.Fatalf("deprecated loop method still present: %s%s", forbidden, method.Type)
		}
	}
}

func assertRequiredLoopMethodSignature(t *testing.T, agentType reflect.Type, name string, wantIn []reflect.Type, wantOut []reflect.Type) {
	t.Helper()

	method, ok := agentType.MethodByName(name)
	if !ok {
		t.Fatalf("required loop method missing: %s", name)
	}

	if method.Type.NumIn() != len(wantIn)+1 {
		t.Fatalf("%s input count = %d, want %d", name, method.Type.NumIn()-1, len(wantIn))
	}
	for i, want := range wantIn {
		if got := method.Type.In(i + 1); got != want {
			t.Fatalf("%s input %d = %v, want %v", name, i, got, want)
		}
	}
	if method.Type.NumOut() != len(wantOut) {
		t.Fatalf("%s output count = %d, want %d", name, method.Type.NumOut(), len(wantOut))
	}
	for i, want := range wantOut {
		if got := method.Type.Out(i); got != want {
			t.Fatalf("%s output %d = %v, want %v", name, i, got, want)
		}
	}
}
```

- [ ] **Step 2: Add RED lifecycle tests**

Create `internal/agent/event_lifecycle_test.go`:

```go
package agent

import (
	"context"
	"reflect"
	"testing"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

func TestAgentStreamEmitsNoToolMessageLifecycle(t *testing.T) {
	provider := chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		return &llms.ChatResponse{Message: llms.Message{
			Role:    llms.RoleAssistant,
			Content: "done without tools",
		}}, nil
	})
	a := New(provider, tools.NewRegistry(), "fake-model")

	events := collectStreamEvents(t, a.Stream(context.Background(), []Message{UserMessage{Content: "say done"}}))

	assertEventTypes(t, events,
		RunStartEvent{},
		TurnStartEvent{},
		MessageStartEvent{},
		MessageDeltaEvent{},
		MessageEndEvent{},
		TurnEndEvent{},
		RunEndEvent{},
	)

	start := events[0].(RunStartEvent)
	if start.RunID == "" {
		t.Fatal("RunStartEvent.RunID is empty")
	}

	turn := events[1].(TurnStartEvent)
	if turn.RunID != start.RunID {
		t.Fatalf("TurnStartEvent.RunID = %q, want %q", turn.RunID, start.RunID)
	}
	if turn.Turn != 1 {
		t.Fatalf("TurnStartEvent.Turn = %d, want 1", turn.Turn)
	}
	if turn.UserMessage.Content != "say done" {
		t.Fatalf("TurnStartEvent.UserMessage.Content = %q", turn.UserMessage.Content)
	}

	delta := events[3].(MessageDeltaEvent)
	if delta.Kind != MessageDeltaText {
		t.Fatalf("MessageDeltaEvent.Kind = %q, want %q", delta.Kind, MessageDeltaText)
	}
	if delta.Delta != "done without tools" {
		t.Fatalf("MessageDeltaEvent.Delta = %q", delta.Delta)
	}

	end := events[4].(MessageEndEvent)
	if end.Message.Content != "done without tools" {
		t.Fatalf("MessageEndEvent.Message.Content = %q", end.Message.Content)
	}
	if len(end.Message.ToolCalls) != 0 {
		t.Fatalf("MessageEndEvent.Message.ToolCalls len = %d, want 0", len(end.Message.ToolCalls))
	}
}

func TestAgentStreamEmitsToolLifecycleAndContinuesWithToolResult(t *testing.T) {
	provider, err := llms.NewFakeProvider(llms.ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}
	recorder := &recordingProvider{inner: provider}
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})
	a := New(recorder, registry, "fake-tool-model")

	events := collectStreamEvents(t, a.Stream(context.Background(), []Message{UserMessage{Content: "use calculator to compute 13 * 7"}}))

	assertEventTypes(t, events,
		RunStartEvent{},
		TurnStartEvent{},
		MessageStartEvent{},
		MessageDeltaEvent{},
		MessageEndEvent{},
		ToolExecutionStartEvent{},
		ToolExecutionEndEvent{},
		MessageStartEvent{},
		MessageDeltaEvent{},
		MessageEndEvent{},
		TurnEndEvent{},
		RunEndEvent{},
	)

	toolDelta := events[3].(MessageDeltaEvent)
	if toolDelta.Kind != MessageDeltaToolCall {
		t.Fatalf("tool delta kind = %q, want %q", toolDelta.Kind, MessageDeltaToolCall)
	}
	if toolDelta.Delta != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("tool delta = %q", toolDelta.Delta)
	}

	assistantWithTool := events[4].(MessageEndEvent).Message
	if len(assistantWithTool.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls len = %d, want 1", len(assistantWithTool.ToolCalls))
	}
	if assistantWithTool.ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("assistant tool name = %q", assistantWithTool.ToolCalls[0].Function.Name)
	}

	toolStart := events[5].(ToolExecutionStartEvent)
	if toolStart.ToolCallID != "call_fake_calculator" {
		t.Fatalf("ToolExecutionStartEvent.ToolCallID = %q", toolStart.ToolCallID)
	}
	if toolStart.ToolName != "calculator" {
		t.Fatalf("ToolExecutionStartEvent.ToolName = %q", toolStart.ToolName)
	}
	if toolStart.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("ToolExecutionStartEvent.Arguments = %q", toolStart.Arguments)
	}

	toolEnd := events[6].(ToolExecutionEndEvent)
	if toolEnd.Error != nil {
		t.Fatalf("ToolExecutionEndEvent.Error = %v, want nil", toolEnd.Error)
	}
	if toolEnd.Result.Content != "91" {
		t.Fatalf("ToolExecutionEndEvent.Result.Content = %q, want 91", toolEnd.Result.Content)
	}
	if toolEnd.Result.IsError {
		t.Fatal("ToolExecutionEndEvent.Result.IsError = true, want false")
	}

	finalAssistant := events[9].(MessageEndEvent).Message
	if finalAssistant.Content != "13 * 7 = 91" {
		t.Fatalf("final assistant content = %q", finalAssistant.Content)
	}
	if len(finalAssistant.ToolCalls) != 0 {
		t.Fatalf("final assistant tool calls len = %d, want 0", len(finalAssistant.ToolCalls))
	}

	if len(recorder.requests) != 2 {
		t.Fatalf("provider requests len = %d, want 2", len(recorder.requests))
	}
	second := recorder.requests[1]
	if len(second.Messages) != 3 {
		t.Fatalf("second provider request messages len = %d, want 3", len(second.Messages))
	}
	if second.Messages[1].Role != llms.RoleAssistant {
		t.Fatalf("second request assistant role = %q", second.Messages[1].Role)
	}
	if second.Messages[2].Role != llms.RoleTool {
		t.Fatalf("second request tool role = %q", second.Messages[2].Role)
	}
	if second.Messages[2].Content != "91" {
		t.Fatalf("second request tool content = %q", second.Messages[2].Content)
	}
}

func assertEventTypes(t *testing.T, events []Event, want ...Event) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("events len = %d, want %d: %#v", len(events), len(want), events)
	}
	for i := range want {
		if gotType, wantType := reflect.TypeOf(events[i]), reflect.TypeOf(want[i]); gotType != wantType {
			t.Fatalf("event[%d] = %T, want %T", i, events[i], want[i])
		}
	}
}
```


- [ ] **Step 3: Run the RED agent API/lifecycle tests**

Run:

```bash
go test ./internal/agent -run 'TestAgentLoopExecutionAPIIsEventStreamOnly|TestAgentStreamEmits' -count=1
```

Expected: FAIL at compile time because `Agent.Stream`, `TurnStartEvent`, `MessageStartEvent`, `MessageDeltaEvent`, `MessageEndEvent`, `ToolExecutionStartEvent`, and `ToolExecutionEndEvent` do not exist yet.

- [ ] **Step 4: Commit the RED tests**

```bash
git add internal/agent/loop_api_surface_regression_test.go internal/agent/event_lifecycle_test.go
git commit -m "test: lock event-first agent lifecycle"
```

---

### Task 2: Implement the agent event model and stream-only loop

**Files:**
- Modify: `internal/agent/events.go`
- Modify: `internal/agent/loop.go`
- Modify later in cleanup: `internal/agent/type.go`

- [ ] **Step 1: Replace the event model**

Replace `internal/agent/events.go` with:

```go
package agent

// Event 是 Agent 对外暴露的强类型运行事件。
type Event interface {
	// AgentEvent 限定事件只由本包定义，避免外部伪造不完整事件。
	AgentEvent()
}

// MessageDeltaKind 标识一段 message 增量属于可见文本、thinking 还是 tool call 参数。
type MessageDeltaKind string

const (
	// MessageDeltaText 表示 assistant 可见文本增量。
	MessageDeltaText MessageDeltaKind = "text"
	// MessageDeltaThinking 表示模型 reasoning/thinking 增量；当前 provider 暂不产生。
	MessageDeltaThinking MessageDeltaKind = "thinking"
	// MessageDeltaToolCall 表示 assistant tool call 参数增量。
	MessageDeltaToolCall MessageDeltaKind = "tool_call"
)

// RunStartEvent 表示 Agent 已开始一次运行。
type RunStartEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
}

// AgentEvent 标记 RunStartEvent 为 Agent 运行事件。
func (RunStartEvent) AgentEvent() {}

// TurnStartEvent 表示 Agent 已开始处理当前用户 turn。
type TurnStartEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// Turn 是 transcript 中从 1 开始计数的当前用户 turn。
	Turn int
	// UserMessage 是触发本轮运行的用户消息。
	UserMessage UserMessage
}

// AgentEvent 标记 TurnStartEvent 为 Agent 运行事件。
func (TurnStartEvent) AgentEvent() {}

// MessageStartEvent 表示一条 assistant message 开始生成。
type MessageStartEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// MessageID 是本条 assistant message 在本次运行内的稳定标识。
	MessageID string
	// Role 是消息角色；当前只会是 "assistant"。
	Role string
}

// AgentEvent 标记 MessageStartEvent 为 Agent 运行事件。
func (MessageStartEvent) AgentEvent() {}

// MessageDeltaEvent 表示一条 assistant message 的增量内容。
type MessageDeltaEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// MessageID 关联对应的 MessageStartEvent 和 MessageEndEvent。
	MessageID string
	// Kind 标识增量类型。
	Kind MessageDeltaKind
	// ContentIndex 是同类内容块在当前 message 中的下标。
	ContentIndex int
	// Delta 是本次输出的增量文本；非 streaming provider 可一次性输出完整内容。
	Delta string
}

// AgentEvent 标记 MessageDeltaEvent 为 Agent 运行事件。
func (MessageDeltaEvent) AgentEvent() {}

// MessageEndEvent 表示一条 assistant message 已完整生成。
type MessageEndEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// MessageID 关联对应的 MessageStartEvent。
	MessageID string
	// Message 是可持久化到 transcript 的完整 assistant message。
	Message AssistantMessage
}

// AgentEvent 标记 MessageEndEvent 为 Agent 运行事件。
func (MessageEndEvent) AgentEvent() {}

// ToolExecutionStartEvent 表示本地工具开始执行。
type ToolExecutionStartEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// ToolCallID 是模型生成的 tool call 标识。
	ToolCallID string
	// ToolName 是被调用的本地工具名称。
	ToolName string
	// Arguments 是模型传入工具的原始 JSON 参数。
	Arguments string
}

// AgentEvent 标记 ToolExecutionStartEvent 为 Agent 运行事件。
func (ToolExecutionStartEvent) AgentEvent() {}

// ToolExecutionEndEvent 表示本地工具已返回结果。
type ToolExecutionEndEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// ToolCallID 是模型生成的 tool call 标识。
	ToolCallID string
	// Result 是可持久化到 transcript 的完整 tool result message。
	Result ToolResultMessage
	// Error 保存工具执行失败时的原始错误；成功时为空。
	Error error
}

// AgentEvent 标记 ToolExecutionEndEvent 为 Agent 运行事件。
func (ToolExecutionEndEvent) AgentEvent() {}

// TurnEndEvent 表示当前用户 turn 已结束。
type TurnEndEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
	// Turn 是 transcript 中从 1 开始计数的当前用户 turn。
	Turn int
}

// AgentEvent 标记 TurnEndEvent 为 Agent 运行事件。
func (TurnEndEvent) AgentEvent() {}

// RunEndEvent 表示 Agent 已成功结束一次运行。
type RunEndEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识。
	RunID string
}

// AgentEvent 标记 RunEndEvent 为 Agent 运行事件。
func (RunEndEvent) AgentEvent() {}

// ErrorEvent 表示 Agent 运行因错误结束。
type ErrorEvent struct {
	// RunID 是本次运行内所有事件共享的稳定标识；输入校验失败时可能为空。
	RunID string
	// Error 保存导致运行结束的错误。
	Error error
}

// AgentEvent 标记 ErrorEvent 为 Agent 运行事件。
func (ErrorEvent) AgentEvent() {}
```

- [ ] **Step 2: Update `loop.go` imports**

`internal/agent/loop.go` imports should become:

```go
import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"harukizmoe/pimoe/internal/llms"
)
```

- [ ] **Step 3: Replace public loop entry points with `Stream`**

Replace the top of `internal/agent/loop.go` through the old `StreamAgentMessages` wrapper with:

```go
var nextRunSequence atomic.Uint64

// Stream 从调用方提供的强语义无状态对话历史继续执行 Agent，并通过 channel 返回运行事件。
func (a *Agent) Stream(ctx context.Context, messages []Message) <-chan Event {
	stream := make(chan Event)
	go func() {
		defer close(stream)
		a.stream(ctx, messages, stream)
	}()
	return stream
}

func newRunID() string {
	return fmt.Sprintf("run-%d", nextRunSequence.Add(1))
}
```

- [ ] **Step 4: Replace `streamAgentMessages` with an event-first `stream` loop**

Replace `func (a *Agent) streamAgentMessages(...)` with:

```go
func (a *Agent) stream(ctx context.Context, messages []Message, stream chan<- Event) {
	if ctx == nil {
		ctx = context.Background()
	}
	emit := func(event Event) bool {
		return emitEvent(ctx, stream, event)
	}

	if len(messages) == 0 {
		emit(ErrorEvent{Error: fmt.Errorf("messages must not be empty")})
		return
	}
	lastMessage, ok := messages[len(messages)-1].(UserMessage)
	if !ok || strings.TrimSpace(lastMessage.Content) == "" {
		emit(ErrorEvent{Error: fmt.Errorf("last message must be a non-empty user message")})
		return
	}

	messages = cloneMessages(messages)
	if _, err := toLLMMessages(messages); err != nil {
		emit(ErrorEvent{Error: err})
		return
	}

	runID := newRunID()
	turn := countUserMessages(messages)
	toolSchemas := a.tools.Schemas()
	trimmedInput := strings.TrimSpace(lastMessage.Content)
	a.logger.Info(ctx, "agent.run.start", "model", a.model, "input", trimmedInput)
	if !emit(RunStartEvent{RunID: runID}) {
		return
	}
	if !emit(TurnStartEvent{RunID: runID, Turn: turn, UserMessage: UserMessage{Content: trimmedInput}}) {
		return
	}

	for chatRound := 0; ; chatRound++ {
		llmMessages, err := toLLMMessages(messages)
		if err != nil {
			emit(ErrorEvent{RunID: runID, Error: err})
			return
		}
		a.logLLMRequest(ctx, chatRound, len(llmMessages), len(toolSchemas))
		response, err := a.provider.Chat(ctx, llms.ChatRequest{
			Model:    a.model,
			Messages: llmMessages,
			Tools:    toolSchemas,
		})
		if err != nil {
			a.logLLMError(ctx, chatRound, err)
			emit(ErrorEvent{RunID: runID, Error: fmt.Errorf("llm chat round %d: %w", chatRound+1, err)})
			return
		}
		if err := ctx.Err(); err != nil {
			emitCancellation(stream, ErrorEvent{RunID: runID, Error: err})
			return
		}

		assistantMessage := AssistantMessage{
			Content:   response.Message.Content,
			ToolCalls: append([]llms.ToolCall(nil), response.Message.ToolCalls...),
		}
		if _, err := toLLMMessage(assistantMessage); err != nil {
			emit(ErrorEvent{RunID: runID, Error: fmt.Errorf("assistant response: %w", err)})
			return
		}

		messageID := fmt.Sprintf("%s-assistant-%d", runID, chatRound+1)
		if !emitAssistantLifecycle(emit, runID, messageID, assistantMessage) {
			return
		}

		if len(assistantMessage.ToolCalls) == 0 {
			messages = append(messages, assistantMessage)
			a.logger.Info(ctx, "agent.run.done", "answer", assistantMessage.Content)
			if !emit(TurnEndEvent{RunID: runID, Turn: turn}) {
				return
			}
			emit(RunEndEvent{RunID: runID})
			return
		}

		if chatRound >= a.maxSteps {
			a.logger.Error(ctx, "agent.max_steps.exceeded", "max_steps", a.maxSteps, "tool_calls", len(assistantMessage.ToolCalls))
			emit(ErrorEvent{RunID: runID, Error: fmt.Errorf("agent max steps exceeded after %d tool-calling rounds", a.maxSteps)})
			return
		}

		a.logger.Info(ctx, "agent.tool_calls.received", "count", len(assistantMessage.ToolCalls))
		messages = append(messages, assistantMessage)
		for _, call := range assistantMessage.ToolCalls {
			a.logger.Debug(ctx, "agent.tool.call", "name", call.Function.Name, "arguments", call.Function.Arguments)
			if !emit(ToolExecutionStartEvent{RunID: runID, ToolName: call.Function.Name, ToolCallID: call.ID, Arguments: call.Function.Arguments}) {
				return
			}

			toolMessage, err := a.runToolCall(ctx, call)
			if err != nil {
				a.logger.Error(ctx, "agent.tool.error", "name", call.Function.Name, "error", err)
			} else {
				a.logger.Debug(ctx, "agent.tool.result", "name", call.Function.Name, "content", toolMessage.Content)
			}
			if !emit(ToolExecutionEndEvent{RunID: runID, ToolCallID: call.ID, Result: toolMessage, Error: err}) {
				return
			}
			messages = append(messages, toolMessage)
		}
	}
}
```

- [ ] **Step 5: Add loop helpers below `stream`**

Add these helpers before `emitEvent`:

```go
func emitAssistantLifecycle(emit func(Event) bool, runID string, messageID string, message AssistantMessage) bool {
	if !emit(MessageStartEvent{RunID: runID, MessageID: messageID, Role: "assistant"}) {
		return false
	}
	if strings.TrimSpace(message.Content) != "" {
		if !emit(MessageDeltaEvent{RunID: runID, MessageID: messageID, Kind: MessageDeltaText, ContentIndex: 0, Delta: message.Content}) {
			return false
		}
	}
	for i, call := range message.ToolCalls {
		if strings.TrimSpace(call.Function.Arguments) == "" {
			continue
		}
		if !emit(MessageDeltaEvent{RunID: runID, MessageID: messageID, Kind: MessageDeltaToolCall, ContentIndex: i, Delta: call.Function.Arguments}) {
			return false
		}
	}
	return emit(MessageEndEvent{RunID: runID, MessageID: messageID, Message: cloneAssistantMessage(message)})
}

func countUserMessages(messages []Message) int {
	turn := 0
	for _, message := range messages {
		if _, ok := message.(UserMessage); ok {
			turn++
		}
	}
	if turn < 1 {
		return 1
	}
	return turn
}

func cloneMessages(messages []Message) []Message {
	out := make([]Message, len(messages))
	for i, message := range messages {
		switch msg := message.(type) {
		case UserMessage:
			out[i] = msg
		case AssistantMessage:
			out[i] = cloneAssistantMessage(msg)
		case ToolResultMessage:
			out[i] = msg
		default:
			out[i] = message
		}
	}
	return out
}

func cloneAssistantMessage(message AssistantMessage) AssistantMessage {
	return AssistantMessage{
		Content:   message.Content,
		ToolCalls: append([]llms.ToolCall(nil), message.ToolCalls...),
	}
}

func emitCancellation(stream chan<- Event, event Event) {
	select {
	case stream <- event:
	default:
	}
}
```

- [ ] **Step 6: Delete old result collector code**

Remove the entire `collectRunResult` function from `internal/agent/loop.go`. Do not move it to another production file.

- [ ] **Step 7: Run the agent lifecycle tests**

Run:

```bash
go test ./internal/agent -run 'TestAgentLoopExecutionAPIIsEventStreamOnly|TestAgentStreamEmits' -count=1
```

Expected: PASS for the two lifecycle tests. Other existing `internal/agent` tests still fail until migrated because they reference `RunResult`, `RunAgentMessages`, `StreamAgentMessages`, or old event types.

- [ ] **Step 8: Commit the agent implementation**

```bash
git add internal/agent/events.go internal/agent/loop.go
git commit -m "feat: stream agent message lifecycle events"
```

---

### Task 3: Add stateful harness session behavior

**Files:**
- Create: `internal/harness/session_test.go`
- Create: `internal/harness/session.go`
- Modify: `internal/harness/events.go`

- [ ] **Step 1: Add RED session tests**

Create `internal/harness/session_test.go`:

```go
package harness

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

func TestSessionPromptPersistsOnlyTerminalMessages(t *testing.T) {
	h := newFakeHarness(t)
	session := h.NewSession()

	events := collectHarnessStreamEvents(t, session.Prompt(context.Background(), "use calculator to compute 13 * 7"))
	assertHarnessEventTypes(t, events,
		RunStartEvent{},
		TurnStartEvent{},
		MessageStartEvent{},
		MessageDeltaEvent{},
		MessageEndEvent{},
		ToolExecutionStartEvent{},
		ToolExecutionEndEvent{},
		MessageStartEvent{},
		MessageDeltaEvent{},
		MessageEndEvent{},
		TurnEndEvent{},
		RunEndEvent{},
	)

	messages := session.Messages()
	if len(messages) != 4 {
		t.Fatalf("Messages() len = %d, want 4: %#v", len(messages), messages)
	}
	if got := messages[0].(agent.UserMessage).Content; got != "use calculator to compute 13 * 7" {
		t.Fatalf("user message content = %q", got)
	}
	assistantWithTool := messages[1].(agent.AssistantMessage)
	if len(assistantWithTool.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls len = %d, want 1", len(assistantWithTool.ToolCalls))
	}
	toolResult := messages[2].(agent.ToolResultMessage)
	if toolResult.ToolCallID != assistantWithTool.ToolCalls[0].ID {
		t.Fatalf("tool result call id = %q, want %q", toolResult.ToolCallID, assistantWithTool.ToolCalls[0].ID)
	}
	if toolResult.Content != "91" {
		t.Fatalf("tool result content = %q, want 91", toolResult.Content)
	}
	finalAssistant := messages[3].(agent.AssistantMessage)
	if finalAssistant.Content != "13 * 7 = 91" {
		t.Fatalf("final assistant content = %q", finalAssistant.Content)
	}
}

func TestSessionMessagesReturnsDefensiveSnapshot(t *testing.T) {
	h := newFakeHarness(t)
	session := h.NewSession()
	collectHarnessStreamEvents(t, session.Prompt(context.Background(), "use calculator to compute 13 * 7"))

	first := session.Messages()
	first[0] = agent.UserMessage{Content: "mutated"}
	assistantMessage := first[1].(agent.AssistantMessage)
	assistantMessage.ToolCalls[0].Function.Arguments = "mutated"
	first[1] = assistantMessage

	second := session.Messages()
	if got := second[0].(agent.UserMessage).Content; got != "use calculator to compute 13 * 7" {
		t.Fatalf("Messages()[0].Content after caller mutation = %q", got)
	}
	secondAssistant := second[1].(agent.AssistantMessage)
	if got := secondAssistant.ToolCalls[0].Function.Arguments; got != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("Messages()[1].ToolCalls[0].Arguments after caller mutation = %q", got)
	}
}

func TestSessionPromptRejectsEmptyInputWithoutTranscriptMutation(t *testing.T) {
	h := newFakeHarness(t)
	session := h.NewSession()

	events := collectHarnessStreamEvents(t, session.Prompt(context.Background(), " \n\t "))
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	errEvent, ok := events[0].(ErrorEvent)
	if !ok {
		t.Fatalf("event[0] = %T, want ErrorEvent", events[0])
	}
	if errEvent.Error == nil || !strings.Contains(errEvent.Error.Error(), "empty input") {
		t.Fatalf("ErrorEvent.Error = %v, want empty input", errEvent.Error)
	}
	if messages := session.Messages(); len(messages) != 0 {
		t.Fatalf("Messages() len = %d, want 0", len(messages))
	}
}

func TestSessionPromptRejectsConcurrentPromptWithoutTranscriptMutation(t *testing.T) {
	provider := newBlockingProvider()
	h := &Harness{agent: agent.New(provider, tools.NewRegistry(), "blocking-model")}
	session := h.NewSession()

	first := session.Prompt(context.Background(), "first prompt")
	<-provider.started

	secondEvents := collectHarnessStreamEvents(t, session.Prompt(context.Background(), "second prompt"))
	if len(secondEvents) != 1 {
		t.Fatalf("second prompt events len = %d, want 1", len(secondEvents))
	}
	errEvent, ok := secondEvents[0].(ErrorEvent)
	if !ok {
		t.Fatalf("second event = %T, want ErrorEvent", secondEvents[0])
	}
	if errEvent.Error == nil || !strings.Contains(errEvent.Error.Error(), "active turn") {
		t.Fatalf("second ErrorEvent.Error = %v, want active turn", errEvent.Error)
	}

	messagesDuringFirstPrompt := session.Messages()
	if len(messagesDuringFirstPrompt) != 1 {
		t.Fatalf("Messages() len while first prompt active = %d, want 1", len(messagesDuringFirstPrompt))
	}
	if got := messagesDuringFirstPrompt[0].(agent.UserMessage).Content; got != "first prompt" {
		t.Fatalf("active user message content = %q", got)
	}

	close(provider.release)
	collectHarnessStreamEvents(t, first)

	messagesAfterFirstPrompt := session.Messages()
	if len(messagesAfterFirstPrompt) != 2 {
		t.Fatalf("Messages() len after first prompt = %d, want 2", len(messagesAfterFirstPrompt))
	}
	if got := messagesAfterFirstPrompt[0].(agent.UserMessage).Content; got != "first prompt" {
		t.Fatalf("final user message content = %q", got)
	}
}

type blockingProvider struct {
	started chan struct{}
	release chan struct{}
}

func newBlockingProvider() *blockingProvider {
	return &blockingProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (p *blockingProvider) Chat(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	select {
	case <-p.release:
		return &llms.ChatResponse{Message: llms.Message{Role: llms.RoleAssistant, Content: "first done"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func assertHarnessEventTypes(t *testing.T, events []Event, want ...Event) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("events len = %d, want %d: %#v", len(events), len(want), events)
	}
	for i := range want {
		if gotType, wantType := reflect.TypeOf(events[i]), reflect.TypeOf(want[i]); gotType != wantType {
			t.Fatalf("event[%d] = %T, want %T", i, events[i], want[i])
		}
	}
}
```


- [ ] **Step 2: Run the RED session tests**

Run:

```bash
go test ./internal/harness -run 'TestSession' -count=1
```

Expected: FAIL at compile time because `NewSession`, `Session`, and new event aliases are not implemented yet.

- [ ] **Step 3: Update harness event aliases**

Replace `internal/harness/events.go` with:

```go
package harness

import "harukizmoe/pimoe/internal/agent"

// Event 描述 Harness 对外转发的 Agent 运行事件。
type Event = agent.Event

// RunStartEvent 表示 Agent 已开始一次运行。
type RunStartEvent = agent.RunStartEvent

// TurnStartEvent 表示 Agent 已开始处理当前用户 turn。
type TurnStartEvent = agent.TurnStartEvent

// MessageStartEvent 表示一条 assistant message 开始生成。
type MessageStartEvent = agent.MessageStartEvent

// MessageDeltaEvent 表示一条 assistant message 的增量内容。
type MessageDeltaEvent = agent.MessageDeltaEvent

// MessageDeltaKind 标识一段 message 增量属于可见文本、thinking 还是 tool call 参数。
type MessageDeltaKind = agent.MessageDeltaKind

const (
	// MessageDeltaText 表示 assistant 可见文本增量。
	MessageDeltaText = agent.MessageDeltaText
	// MessageDeltaThinking 表示模型 reasoning/thinking 增量。
	MessageDeltaThinking = agent.MessageDeltaThinking
	// MessageDeltaToolCall 表示 assistant tool call 参数增量。
	MessageDeltaToolCall = agent.MessageDeltaToolCall
)

// MessageEndEvent 表示一条 assistant message 已完整生成。
type MessageEndEvent = agent.MessageEndEvent

// ToolExecutionStartEvent 表示本地工具开始执行。
type ToolExecutionStartEvent = agent.ToolExecutionStartEvent

// ToolExecutionEndEvent 表示本地工具已返回结果。
type ToolExecutionEndEvent = agent.ToolExecutionEndEvent

// TurnEndEvent 表示当前用户 turn 已结束。
type TurnEndEvent = agent.TurnEndEvent

// RunEndEvent 表示 Agent 已成功结束一次运行。
type RunEndEvent = agent.RunEndEvent

// ErrorEvent 表示 Agent 运行因错误结束。
type ErrorEvent = agent.ErrorEvent
```

- [ ] **Step 4: Implement `Session`**

Create `internal/harness/session.go`:

```go
package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/llms"
)

// Session 保存一次多 turn Agent 对话的内存态 transcript 和运行控制。
type Session struct {
	mu        sync.Mutex
	agent     *agent.Agent
	messages  []agent.Message
	cancel    context.CancelFunc
	listeners map[chan Event]struct{}
}

// NewSession 创建一个持有独立 transcript 的 Agent 会话。
func (h *Harness) NewSession() *Session {
	return &Session{
		agent:     h.agent,
		listeners: make(map[chan Event]struct{}),
	}
}

// Prompt 追加用户输入并启动一轮 Agent 运行；返回的 channel 只包含本轮事件。
func (s *Session) Prompt(ctx context.Context, input string) <-chan Event {
	if ctx == nil {
		ctx = context.Background()
	}
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return closedErrorStream(fmt.Errorf("empty input"))
	}

	ctx, cancel := context.WithCancel(ctx)
	userMessage := agent.UserMessage{Content: trimmed}

	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		cancel()
		return closedErrorStream(fmt.Errorf("active turn already running"))
	}
	s.cancel = cancel
	s.messages = append(s.messages, userMessage)
	snapshot := cloneSessionMessages(s.messages)
	s.mu.Unlock()

	out := make(chan Event)
	go s.runPrompt(ctx, cancel, snapshot, out)
	return out
}

// Events 订阅 session 后续事件；订阅者必须持续读取以避免被移除。
func (s *Session) Events() <-chan Event {
	ch := make(chan Event, 64)
	s.mu.Lock()
	s.listeners[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

// Messages 返回当前 transcript 的防御性快照。
func (s *Session) Messages() []agent.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSessionMessages(s.messages)
}

// Cancel 取消当前运行；没有运行时是 no-op。
func (s *Session) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Session) runPrompt(ctx context.Context, cancel context.CancelFunc, snapshot []agent.Message, out chan<- Event) {
	defer close(out)
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	for event := range s.agent.Stream(ctx, snapshot) {
		s.applyTerminalEvent(event)
		s.publish(event)
		if !emitHarnessEvent(ctx, out, event) {
			return
		}
	}
}

func (s *Session) applyTerminalEvent(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch event := event.(type) {
	case MessageEndEvent:
		s.messages = append(s.messages, cloneAssistantMessage(event.Message))
	case ToolExecutionEndEvent:
		s.messages = append(s.messages, event.Result)
	}
}

func (s *Session) publish(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for listener := range s.listeners {
		select {
		case listener <- event:
		default:
			close(listener)
			delete(s.listeners, listener)
		}
	}
}

func closedErrorStream(err error) <-chan Event {
	stream := make(chan Event, 1)
	stream <- ErrorEvent{Error: err}
	close(stream)
	return stream
}

func cloneSessionMessages(messages []agent.Message) []agent.Message {
	out := make([]agent.Message, len(messages))
	for i, message := range messages {
		switch msg := message.(type) {
		case agent.UserMessage:
			out[i] = msg
		case agent.AssistantMessage:
			out[i] = cloneAssistantMessage(msg)
		case agent.ToolResultMessage:
			out[i] = msg
		default:
			out[i] = message
		}
	}
	return out
}

func cloneAssistantMessage(message agent.AssistantMessage) agent.AssistantMessage {
	return agent.AssistantMessage{
		Content:   message.Content,
		ToolCalls: append([]llms.ToolCall(nil), message.ToolCalls...),
	}
}
```

- [ ] **Step 5: Run session tests**

Run:

```bash
go test ./internal/harness -run 'TestSession' -count=1
```

Expected: PASS for `TestSession*`. Other harness tests still fail until old `Run`/`Stream` tests are migrated.

- [ ] **Step 6: Commit session implementation**

```bash
git add internal/harness/session.go internal/harness/events.go internal/harness/session_test.go
git commit -m "feat: add stateful harness session"
```

---

### Task 4: Migrate harness tests and remove old harness execution API

**Files:**
- Modify: `internal/harness/harness.go`
- Modify: `internal/harness/harness_test.go`
- Modify: `internal/harness/stream_contract_test.go`

- [ ] **Step 1: Add a test helper that derives the final answer from events**

In `internal/harness/harness_test.go`, add this helper near `newFakeHarness`:

```go
func collectHarnessAnswer(t *testing.T, stream <-chan Event) string {
	t.Helper()
	var answer string
	for event := range stream {
		switch event := event.(type) {
		case MessageEndEvent:
			if len(event.Message.ToolCalls) == 0 {
				answer = event.Message.Content
			}
		case ErrorEvent:
			if event.Error != nil {
				t.Fatalf("stream error = %v", event.Error)
			}
		}
	}
	return answer
}
```

- [ ] **Step 2: Update config tests to use `Session`**

Replace calls like:

```go
got, err := h.Run(context.Background(), "use calculator to compute 13 * 7")
```

with:

```go
answer := collectHarnessAnswer(t, h.NewSession().Prompt(context.Background(), "use calculator to compute 13 * 7"))
```

Replace assertions on `got.Answer` with:

```go
if answer != "13 * 7 = 91" {
	t.Fatalf("answer = %q, want %q", answer, "13 * 7 = 91")
}
```

Delete assertions on `RunResult.ToolRounds`, `RunResult.Steps`, and `RunResult.Messages` from harness config tests. Tool trace behavior is covered by session lifecycle and CLI collector tests.

- [ ] **Step 3: Replace empty input tests**

Replace `TestRunRejectsEmptyOrWhitespaceOnlyInput` with:

```go
func TestSessionPromptRejectsEmptyOrWhitespaceOnlyInput(t *testing.T) {
	h := newFakeHarness(t)

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "spaces", input: "   "},
		{name: "mixed_whitespace", input: " \n\t "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := h.NewSession()
			events := collectHarnessStreamEvents(t, session.Prompt(context.Background(), tt.input))
			if len(events) != 1 {
				t.Fatalf("Prompt(%q) events len = %d, want 1", tt.input, len(events))
			}
			errEvent, ok := events[0].(ErrorEvent)
			if !ok {
				t.Fatalf("Prompt(%q) event = %T, want ErrorEvent", tt.input, events[0])
			}
			if errEvent.Error == nil || !strings.Contains(errEvent.Error.Error(), "empty input") {
				t.Fatalf("Prompt(%q) error = %v, want empty input", tt.input, errEvent.Error)
			}
			if messages := session.Messages(); len(messages) != 0 {
				t.Fatalf("Messages() len = %d, want 0", len(messages))
			}
		})
	}
}
```

- [ ] **Step 4: Replace history continuation harness tests with session transcript tests**

Delete `TestRunAgentMessagesRejectsEmptyFinalUserMessageAfterTrim` and `TestRunAgentMessagesContinuesHistoryEndingWithUserMessage`. `Session.Prompt` now owns appending user messages; explicit caller-owned history continuation remains an `agent.Stream` concern and is tested in `internal/agent`.

- [ ] **Step 5: Remove old harness execution helpers**

In `internal/harness/harness.go`, delete these methods:

```go
func (h *Harness) Stream(ctx context.Context, input string) <-chan Event
func (h *Harness) Run(ctx context.Context, input string) (*agent.RunResult, error)
func (h *Harness) RunAgentMessages(ctx context.Context, messages []agent.Message) (*agent.RunResult, error)
```

Keep `New`, `Config`, and `Harness` construction code unchanged.

- [ ] **Step 6: Replace stream contract tests**

In `internal/harness/stream_contract_test.go`:

- Delete `harnessStreamer` and `var _ harnessStreamer = (*Harness)(nil)`.
- Delete `TestRunReturnsToolTraceWithoutEventHistory`.
- Rename `TestStreamReturnsTypedToolCallingEvents` to `TestSessionPromptReturnsTypedToolCallingEvents` and change the stream source to:

```go
events := collectHarnessStreamEvents(t, h.NewSession().Prompt(context.Background(), "use calculator to compute 13 * 7"))
```

- Update the expected event sequence to the same 12-event lifecycle from `TestSessionPromptPersistsOnlyTerminalMessages`.
- Replace old `ToolCallEvent`, `ToolResultEvent`, and `FinalEvent` assertions with `ToolExecutionStartEvent`, `ToolExecutionEndEvent`, and final `MessageEndEvent` assertions.

- [ ] **Step 7: Run harness tests**

Run:

```bash
go test ./internal/harness -count=1
```

Expected: PASS for the harness package.

- [ ] **Step 8: Commit harness migration**

```bash
git add internal/harness/harness.go internal/harness/harness_test.go internal/harness/stream_contract_test.go
git commit -m "refactor: route harness execution through sessions"
```

---

### Task 5: Move CLI output to a local event collector

**Files:**
- Modify: `cmd/cli/main_test.go`
- Modify: `cmd/cli/main.go`

- [ ] **Step 1: Replace CLI formatter tests with event collector tests**

In `cmd/cli/main_test.go`, replace the imports with:

```go
import (
	"errors"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/harness"
)
```

Replace the three `TestFormatRunResult*` tests with:

```go
func TestCollectRunOutputAnswerOnlyReturnsAnswerWithTrailingNewline(t *testing.T) {
	output, err := collectRunOutput(eventStream(
		harness.MessageEndEvent{Message: agent.AssistantMessage{Content: "done without tools"}},
		harness.RunEndEvent{RunID: "run-1"},
	))
	if err != nil {
		t.Fatalf("collectRunOutput() error = %v", err)
	}

	got := formatRunOutput(output, false)

	const want = "done without tools\n"
	if got != want {
		t.Fatalf("formatRunOutput(answer only) = %q, want %q", got, want)
	}
	if strings.Contains(got, "tool=") || strings.Contains(got, "arguments=") || strings.Contains(got, "result=") || strings.Contains(got, "error=") {
		t.Fatalf("formatRunOutput(answer only) leaked trace fields: %q", got)
	}
}

func TestCollectRunOutputTraceIncludesSuccessfulToolSteps(t *testing.T) {
	output, err := collectRunOutput(eventStream(
		harness.ToolExecutionStartEvent{ToolCallID: "call-1", ToolName: "calculator", Arguments: `{"a":2,"b":3,"op":"add"}`},
		harness.ToolExecutionEndEvent{ToolCallID: "call-1", Result: agent.ToolResultMessage{ToolCallID: "call-1", ToolName: "calculator", Content: "5"}},
		harness.ToolExecutionStartEvent{ToolCallID: "call-2", ToolName: "calculator", Arguments: `{"a":5,"b":4,"op":"multiply"}`},
		harness.ToolExecutionEndEvent{ToolCallID: "call-2", Result: agent.ToolResultMessage{ToolCallID: "call-2", ToolName: "calculator", Content: "20"}},
		harness.MessageEndEvent{Message: agent.AssistantMessage{Content: "final answer: 20"}},
		harness.RunEndEvent{RunID: "run-1"},
	))
	if err != nil {
		t.Fatalf("collectRunOutput() error = %v", err)
	}

	got := formatRunOutput(output, true)

	if !strings.HasPrefix(got, "final answer: 20\n") {
		t.Fatalf("formatRunOutput(trace) prefix = %q, want answer followed by newline", got)
	}
	for _, want := range []string{
		"tool=calculator",
		`arguments={"a":2,"b":3,"op":"add"}`,
		"result=5",
		`arguments={"a":5,"b":4,"op":"multiply"}`,
		"result=20",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatRunOutput(trace) missing %q in %q", want, got)
		}
	}
}

func TestCollectRunOutputTraceIncludesToolErrors(t *testing.T) {
	toolErr := errors.New("upstream unavailable")
	output, err := collectRunOutput(eventStream(
		harness.ToolExecutionStartEvent{ToolCallID: "call-weather", ToolName: "weather", Arguments: `{"city":"Tokyo"}`},
		harness.ToolExecutionEndEvent{ToolCallID: "call-weather", Result: agent.ToolResultMessage{ToolCallID: "call-weather", ToolName: "weather", Content: `tool "weather" failed: upstream unavailable`, IsError: true}, Error: toolErr},
		harness.MessageEndEvent{Message: agent.AssistantMessage{Content: "could not complete weather lookup"}},
		harness.RunEndEvent{RunID: "run-1"},
	))
	if err != nil {
		t.Fatalf("collectRunOutput() error = %v", err)
	}

	got := formatRunOutput(output, true)

	if !strings.HasPrefix(got, "could not complete weather lookup\n") {
		t.Fatalf("formatRunOutput(trace error) prefix = %q, want answer followed by newline", got)
	}
	for _, want := range []string{
		"tool=weather",
		`arguments={"city":"Tokyo"}`,
		"error=upstream unavailable",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatRunOutput(trace error) missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "result=<nil>") {
		t.Fatalf("formatRunOutput(trace error) exposed nil result placeholder: %q", got)
	}
}

func eventStream(events ...harness.Event) <-chan harness.Event {
	stream := make(chan harness.Event, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return stream
}
```

- [ ] **Step 2: Run RED CLI tests**

Run:

```bash
go test ./cmd/cli -run 'TestCollectRunOutput|TestFormatRunOutput' -count=1
```

Expected: FAIL at compile time because `collectRunOutput`, `formatRunOutput`, and local output types do not exist yet.

- [ ] **Step 3: Update `main.go` imports**

Remove the `agent` import from `cmd/cli/main.go`; keep `harness`.

The import block should be:

```go
import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"harukizmoe/pimoe/internal/harness"
	"harukizmoe/pimoe/internal/logger"
)
```

- [ ] **Step 4: Route main through session events**

Replace:

```go
result, err := runner.Run(context.Background(), input)
if err != nil {
	log.Fatal(err)
}

fmt.Print(formatRunResult(result, opts.includeTrace))
```

with:

```go
session := runner.NewSession()
output, err := collectRunOutput(session.Prompt(context.Background(), input))
if err != nil {
	log.Fatal(err)
}

fmt.Print(formatRunOutput(output, opts.includeTrace))
```

- [ ] **Step 5: Replace `formatRunResult` with event output collector and formatter**

Replace `formatRunResult` in `cmd/cli/main.go` with:

```go
type runOutput struct {
	Answer string
	Steps  []toolStep
}

type toolStep struct {
	ToolCallID string
	ToolName   string
	Arguments  string
	Result     string
	Error      string
}

func collectRunOutput(events <-chan harness.Event) (runOutput, error) {
	var output runOutput
	stepByCallID := make(map[string]int)

	for event := range events {
		switch event := event.(type) {
		case harness.ToolExecutionStartEvent:
			stepByCallID[event.ToolCallID] = len(output.Steps)
			output.Steps = append(output.Steps, toolStep{
				ToolCallID: event.ToolCallID,
				ToolName:   event.ToolName,
				Arguments:  event.Arguments,
			})
		case harness.ToolExecutionEndEvent:
			stepIndex, ok := stepByCallID[event.ToolCallID]
			if !ok {
				stepIndex = len(output.Steps)
				stepByCallID[event.ToolCallID] = stepIndex
				output.Steps = append(output.Steps, toolStep{ToolCallID: event.ToolCallID, ToolName: event.Result.ToolName})
			}
			if event.Error != nil {
				output.Steps[stepIndex].Error = event.Error.Error()
			} else if event.Result.IsError {
				output.Steps[stepIndex].Error = event.Result.Content
			} else {
				output.Steps[stepIndex].Result = event.Result.Content
			}
		case harness.MessageEndEvent:
			if len(event.Message.ToolCalls) == 0 {
				output.Answer = event.Message.Content
			}
		case harness.ErrorEvent:
			if event.Error != nil {
				return output, event.Error
			}
		}
	}

	return output, nil
}

func formatRunOutput(output runOutput, includeTrace bool) string {
	var builder strings.Builder
	builder.WriteString(output.Answer)
	builder.WriteByte('\n')

	if !includeTrace {
		return builder.String()
	}

	for _, step := range output.Steps {
		builder.WriteString("\n")
		builder.WriteString("tool=")
		builder.WriteString(step.ToolName)
		builder.WriteByte('\n')
		builder.WriteString("arguments=")
		builder.WriteString(step.Arguments)
		builder.WriteByte('\n')
		if step.Error != "" {
			builder.WriteString("error=")
			builder.WriteString(step.Error)
			builder.WriteByte('\n')
			continue
		}
		builder.WriteString("result=")
		builder.WriteString(step.Result)
		builder.WriteByte('\n')
	}

	return builder.String()
}
```

- [ ] **Step 6: Run CLI tests**

Run:

```bash
go test ./cmd/cli -count=1
```

Expected: PASS for the CLI package.

- [ ] **Step 7: Commit CLI migration**

```bash
git add cmd/cli/main.go cmd/cli/main_test.go
git commit -m "refactor: derive cli output from events"
```

---

### Task 6: Remove `RunResult` leftovers and verify the cutover

**Files:**
- Modify: `internal/agent/type.go`
- Modify/delete: old tests in `internal/agent/loop_test.go`
- Modify/delete: stale references in `internal/harness/*_test.go`
- Modify/delete: stale references in `cmd/cli/*_test.go`

- [ ] **Step 1: Delete obsolete result types**

In `internal/agent/type.go`, delete:

```go
// RunResult 表示一次 Agent 运行的结构化结果。
type RunResult struct {
	Answer string
	ToolRounds int
	Messages []Message
	Steps []Step
}

// Step 表示一次本地工具执行的 trace 记录。
type Step struct {
	ToolCallID string
	ToolName string
	Arguments string
	Result string
	Error string
}
```

If `RunRequest` and `RunResponse` have no remaining references, delete them too. Keep `type.go` only if at least one exported type remains in it; otherwise remove the file.

- [ ] **Step 2: Replace agent tests that depended on `RunResult`**

In `internal/agent/loop_test.go`:

- Delete helper interface `runAgentMessagesRunner` and helpers `runAgentMessages`, `runAgentText`, `runAgentResult`.
- Replace `streamAgentText` with:

```go
func streamAgentText(a *Agent, ctx context.Context, input string) <-chan Event {
	return a.Stream(ctx, []Message{UserMessage{Content: input}})
}
```

- Convert tests that only assert final answer to collect the final no-tool `MessageEndEvent`:

```go
func collectFinalAnswer(t *testing.T, stream <-chan Event) string {
	t.Helper()
	var answer string
	for event := range stream {
		switch event := event.(type) {
		case MessageEndEvent:
			if len(event.Message.ToolCalls) == 0 {
				answer = event.Message.Content
			}
		case ErrorEvent:
			if event.Error != nil {
				t.Fatalf("stream error = %v", event.Error)
			}
		}
	}
	return answer
}
```

- Convert transcript assertions to consume `MessageEndEvent` and `ToolExecutionEndEvent` rather than `RunResult.Messages`.
- Convert tool trace assertions to event assertions, not `RunResult.Steps`.
- Delete duplicate tests that are now covered by `event_lifecycle_test.go` and `session_test.go`.

- [ ] **Step 3: Use the built-in Grep tool to find stale symbols**

Use the Grep tool, scoped to `internal;cmd`, with this regex:

```text
RunResult|RunAgentMessages|StreamAgentMessages|FinalEvent|ToolCallEvent|ToolResultEvent|LLMRequestEvent|LLMErrorEvent|formatRunResult
```

Expected: no matches in production or tests after migration. If matches remain, update or delete them before continuing.

- [ ] **Step 4: Run focused package tests**

Run:

```bash
go test ./internal/agent ./internal/harness ./cmd/cli -count=1
```

Expected: PASS.

- [ ] **Step 5: Run project tests**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit cleanup**

```bash
git add internal/agent internal/harness cmd/cli
git commit -m "refactor: remove run result execution path"
```

---

## Self-Review Checklist

- Spec coverage:
  - Agent event-first API: Tasks 1, 2, 6.
  - Harness stateful session owner: Tasks 3, 4.
  - Transcript persistence only at terminal events: Task 3 tests and implementation.
  - CLI synchronous answer as adapter-derived value: Task 5.
  - `RunResult` removal: Task 6.
  - Error/cancel/concurrent prompt behavior: Task 3 covers empty and concurrent prompt; Task 2 emits cancellation best-effort; existing provider/tool error tests must be migrated in Task 6.
- Placeholder scan:
  - No `TBD`, `TODO`, `implement later`, or unnamed edge cases.
  - Every code-changing task includes concrete code or exact deletion/replacement rules.
- Type consistency:
  - Event types use `MessageDeltaKind`, `MessageEndEvent.Message agent.AssistantMessage`, and `ToolExecutionEndEvent.Result agent.ToolResultMessage` consistently.
  - Harness aliases mirror `internal/agent/events.go`.
  - CLI collector consumes `harness.Event` and does not import or reference `agent.RunResult`.
