package agent

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

type recordingProvider struct {
	inner    llms.Provider
	requests []llms.ChatRequest
}

func (p *recordingProvider) ChatStream(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
	messages := make([]llms.Message, len(req.Messages))
	for i, msg := range req.Messages {
		toolCalls := append([]llms.ToolCall(nil), msg.ToolCalls...)
		messages[i] = llms.Message{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCalls:  toolCalls,
			ToolCallID: msg.ToolCallID,
		}
	}

	copied := llms.ChatRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    append([]llms.Tool(nil), req.Tools...),
	}
	p.requests = append(p.requests, copied)

	return p.inner.ChatStream(ctx, req)
}

type streamFunc func(context.Context, llms.ChatRequest) (<-chan llms.ChatStreamEvent, error)

func (f streamFunc) ChatStream(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
	return f(ctx, req)
}

func assistantTextDelta(content string) llms.ChatStreamEvent {
	return llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Role: llms.RoleAssistant, Content: content}}
}

func assistantToolCallDelta(index int, call llms.ToolCall) llms.ChatStreamEvent {
	return llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCallDelta{{
		Index: index,
		ID:    call.ID,
		Type:  call.Type,
		Function: llms.ToolCallFunctionDelta{
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		},
	}}}}
}

func assistantDone(message llms.Message) llms.ChatStreamEvent {
	return llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDone, Message: message}
}

func streamAgentText(a *Agent, ctx context.Context, input string) <-chan Event {
	return a.Stream(ctx, []Message{UserMessage{Content: input}})
}

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

func assertMessagesEqual(t *testing.T, got, want []llms.Message) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("messages len = %d, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i].Role != want[i].Role {
			t.Fatalf("messages[%d].Role = %q, want %q", i, got[i].Role, want[i].Role)
		}
		if got[i].Content != want[i].Content {
			t.Fatalf("messages[%d].Content = %q, want %q", i, got[i].Content, want[i].Content)
		}
		if got[i].ToolCallID != want[i].ToolCallID {
			t.Fatalf("messages[%d].ToolCallID = %q, want %q", i, got[i].ToolCallID, want[i].ToolCallID)
		}
		if len(got[i].ToolCalls) != len(want[i].ToolCalls) {
			t.Fatalf("messages[%d].ToolCalls len = %d, want %d", i, len(got[i].ToolCalls), len(want[i].ToolCalls))
		}

		for j := range want[i].ToolCalls {
			gotCall := got[i].ToolCalls[j]
			wantCall := want[i].ToolCalls[j]
			if gotCall.ID != wantCall.ID {
				t.Fatalf("messages[%d].ToolCalls[%d].ID = %q, want %q", i, j, gotCall.ID, wantCall.ID)
			}
			if gotCall.Type != wantCall.Type {
				t.Fatalf("messages[%d].ToolCalls[%d].Type = %q, want %q", i, j, gotCall.Type, wantCall.Type)
			}
			if gotCall.Function.Name != wantCall.Function.Name {
				t.Fatalf("messages[%d].ToolCalls[%d].Function.Name = %q, want %q", i, j, gotCall.Function.Name, wantCall.Function.Name)
			}
			if gotCall.Function.Arguments != wantCall.Function.Arguments {
				t.Fatalf("messages[%d].ToolCalls[%d].Function.Arguments = %q, want %q", i, j, gotCall.Function.Arguments, wantCall.Function.Arguments)
			}
		}
	}
}

type recordingLogger struct {
	messages []string
}

func (l *recordingLogger) Debug(ctx context.Context, msg string, attrs ...any) {
	l.messages = append(l.messages, msg)
}

func (l *recordingLogger) Info(ctx context.Context, msg string, attrs ...any) {
	l.messages = append(l.messages, msg)
}

func (l *recordingLogger) Error(ctx context.Context, msg string, attrs ...any) {
	l.messages = append(l.messages, msg)
}

func (l *recordingLogger) contains(msg string) bool {
	for _, got := range l.messages {
		if got == msg {
			return true
		}
	}
	return false
}

func TestAgentRunExecutesToolCall(t *testing.T) {
	provider, err := llms.NewFakeProvider(llms.ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	recorder := &recordingProvider{inner: provider}
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	a := New(recorder, registry, "fake-tool-model")
	got := collectFinalAnswer(t, streamAgentText(a, context.Background(), "use calculator to compute 13 * 7"))
	if got != "13 * 7 = 91" {
		t.Fatalf("answer = %q", got)
	}

	if len(recorder.requests) != 2 {
		t.Fatalf("chat requests len = %d", len(recorder.requests))
	}

	first := recorder.requests[0]
	if len(first.Messages) != 1 {
		t.Fatalf("first messages len = %d", len(first.Messages))
	}
	if first.Messages[0].Role != llms.RoleUser {
		t.Fatalf("first user role = %q", first.Messages[0].Role)
	}
	if first.Messages[0].Content != "use calculator to compute 13 * 7" {
		t.Fatalf("first user content = %q", first.Messages[0].Content)
	}
	if len(first.Tools) != 1 {
		t.Fatalf("first tools len = %d", len(first.Tools))
	}
	if first.Tools[0].Function.Name != "calculator" {
		t.Fatalf("first tool name = %q", first.Tools[0].Function.Name)
	}

	second := recorder.requests[1]
	if len(second.Messages) != 3 {
		t.Fatalf("second messages len = %d", len(second.Messages))
	}
	assistant := second.Messages[1]
	if assistant.Role != llms.RoleAssistant {
		t.Fatalf("assistant role = %q", assistant.Role)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls len = %d", len(assistant.ToolCalls))
	}
	call := assistant.ToolCalls[0]
	if call.Function.Name != "calculator" {
		t.Fatalf("assistant tool name = %q", call.Function.Name)
	}
	if call.Function.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("assistant tool args = %q", call.Function.Arguments)
	}

	toolMessage := second.Messages[2]
	if toolMessage.Role != llms.RoleTool {
		t.Fatalf("tool role = %q", toolMessage.Role)
	}
	if toolMessage.ToolCallID != call.ID {
		t.Fatalf("tool call id = %q, want %q", toolMessage.ToolCallID, call.ID)
	}
	if toolMessage.Content != "91" {
		t.Fatalf("tool content = %q", toolMessage.Content)
	}
}

func TestAgentRunLogsToolCallingFlow(t *testing.T) {
	provider, err := llms.NewFakeProvider(llms.ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})
	log := &recordingLogger{}

	a := NewWithLogger(provider, registry, "fake-tool-model", log)
	got := collectFinalAnswer(t, streamAgentText(a, context.Background(), "use calculator to compute 13 * 7"))
	if got != "13 * 7 = 91" {
		t.Fatalf("answer = %q", got)
	}

	for _, want := range []string{
		"agent.run.start",
		"agent.llm.first.request",
		"agent.tool_calls.received",
		"agent.tool.call",
		"agent.tool.result",
		"agent.llm.final.request",
		"agent.run.done",
	} {
		if !log.contains(want) {
			t.Fatalf("logged messages = %v, want %q", log.messages, want)
		}
	}
}

func TestAgentRunDefaultAllowsSecondRoundToolCall(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		round++
		switch round {
		case 1:
			call := calculatorToolCall("call_first_round", `{"a":13,"b":7,"op":"mul"}`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		case 2:
			call := calculatorToolCall("call_second_round", `{"a":1,"b":2,"op":"add"}`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		case 3:
			return streamEvents(
				assistantTextDelta("second round completed"),
				assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "second round completed"}),
			), nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := New(provider, registry, "fake-tool-model")
	got := collectFinalAnswer(t, streamAgentText(a, context.Background(), "use calculator twice"))
	if got != "second round completed" {
		t.Fatalf("answer = %q", got)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("chat requests len = %d", len(provider.requests))
	}
}

func calculatorToolCall(id, arguments string) llms.ToolCall {
	return llms.ToolCall{
		ID:   id,
		Type: "function",
		Function: llms.ToolCallFunction{
			Name:      "calculator",
			Arguments: arguments,
		},
	}
}

func TestAgentRunSupportsMultipleToolRoundsWithinMaxSteps(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		round++
		switch round {
		case 1:
			call := calculatorToolCall("call_step_1", `{"a":2,"b":3,"op":"add"}`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		case 2:
			if got := req.Messages[len(req.Messages)-1]; got.Role != llms.RoleTool || got.Content != "5" {
				t.Fatalf("second request last message = %#v, want tool result 5", got)
			}
			call := calculatorToolCall("call_step_2", `{"a":5,"b":4,"op":"mul"}`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		case 3:
			if got := req.Messages[len(req.Messages)-1]; got.Role != llms.RoleTool || got.Content != "20" {
				t.Fatalf("third request last message = %#v, want tool result 20", got)
			}
			return streamEvents(
				assistantTextDelta("final answer: 20"),
				assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "final answer: 20"}),
			), nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := NewWithOptions(provider, registry, "fake-tool-model", Options{MaxSteps: 2})
	got := collectFinalAnswer(t, streamAgentText(a, context.Background(), "compute (2 + 3) * 4"))
	if got != "final answer: 20" {
		t.Fatalf("answer = %q", got)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("chat requests len = %d", len(provider.requests))
	}
}

func TestAgentRunReturnsMaxStepsErrorWhenToolLoopExceedsLimit(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		round++
		switch round {
		case 1:
			call := calculatorToolCall("call_step_1", `{"a":2,"b":3,"op":"add"}`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		case 2:
			if got := req.Messages[len(req.Messages)-1]; got.Role != llms.RoleTool || got.Content != "5" {
				t.Fatalf("second request last message = %#v, want tool result 5", got)
			}
			call := calculatorToolCall("call_step_2", `{"a":5,"b":4,"op":"mul"}`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := NewWithOptions(provider, registry, "fake-tool-model", Options{MaxSteps: 1})
	events := collectStreamEvents(t, streamAgentText(a, context.Background(), "compute (2 + 3) * 4"))
	var streamErr error
	for _, event := range events {
		if errEvent, ok := event.(ErrorEvent); ok {
			streamErr = errEvent.Error
		}
	}
	if streamErr == nil {
		t.Fatal("stream error = nil, want max-steps error")
	}
	if !strings.Contains(streamErr.Error(), "max steps") {
		t.Fatalf("stream error = %v, want max steps message", streamErr)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("chat requests len = %d", len(provider.requests))
	}
}

func collectStreamEvents(t *testing.T, stream <-chan Event) []Event {
	t.Helper()

	var events []Event
	for event := range stream {
		events = append(events, event)
	}

	return events
}

func TestAgentStreamReturnsAnswerWithoutToolCalls(t *testing.T) {
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(
			assistantTextDelta("done without tools"),
			assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done without tools"}),
		), nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got := collectFinalAnswer(t, streamAgentText(a, context.Background(), "just answer directly"))
	if got != "done without tools" {
		t.Fatalf("answer = %q", got)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("chat requests len = %d, want 1", len(provider.requests))
	}
}

func TestAgentStreamWithProvidedHistoryEmitsToolRoundEvents(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	history := []Message{
		UserMessage{Content: "what is 4 * 4?"},
		AssistantMessage{Content: "4 * 4 = 16."},
		UserMessage{Content: "now multiply that by 5"},
	}
	wantHistory := []llms.Message{
		{Role: llms.RoleUser, Content: "what is 4 * 4?"},
		{Role: llms.RoleAssistant, Content: "4 * 4 = 16."},
		{Role: llms.RoleUser, Content: "now multiply that by 5"},
	}
	assistantToolCall := llms.Message{
		Role: llms.RoleAssistant,
		ToolCalls: []llms.ToolCall{
			calculatorToolCall("call_history_mul", `{"a":16,"b":5,"op":"mul"}`),
		},
	}
	toolResult := llms.Message{Role: llms.RoleTool, Content: "80", ToolCallID: "call_history_mul"}

	round := 0
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		round++
		switch round {
		case 1:
			assertMessagesEqual(t, req.Messages, wantHistory)
			return streamEvents(
				assistantToolCallDelta(0, assistantToolCall.ToolCalls[0]),
				assistantDone(assistantToolCall),
			), nil
		case 2:
			want := append(append([]llms.Message{}, wantHistory...), assistantToolCall, toolResult)
			assertMessagesEqual(t, req.Messages, want)
			return streamEvents(
				assistantTextDelta("16 * 5 = 80"),
				assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "16 * 5 = 80"}),
			), nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := New(provider, registry, "fake-tool-model")
	events := collectStreamEvents(t, a.Stream(context.Background(), history))

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
	if len(provider.requests) != 2 {
		t.Fatalf("chat requests len = %d, want 2", len(provider.requests))
	}

	assistant := events[4].(MessageEndEvent).Message
	if assistant.Content != "" {
		t.Fatalf("assistant tool-call content = %q, want empty", assistant.Content)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls len = %d, want 1", len(assistant.ToolCalls))
	}
	if gotCall := assistant.ToolCalls[0]; gotCall.ID != "call_history_mul" || gotCall.Function.Name != "calculator" || gotCall.Function.Arguments != `{"a":16,"b":5,"op":"mul"}` {
		t.Fatalf("assistant tool call = %#v", gotCall)
	}

	toolCall := events[5].(ToolExecutionStartEvent)
	if toolCall.ToolCallID != "call_history_mul" {
		t.Fatalf("tool call id = %q, want call_history_mul", toolCall.ToolCallID)
	}
	if toolCall.ToolName != "calculator" {
		t.Fatalf("tool name = %q, want calculator", toolCall.ToolName)
	}
	if toolCall.Arguments != `{"a":16,"b":5,"op":"mul"}` {
		t.Fatalf("tool arguments = %q", toolCall.Arguments)
	}

	toolEnd := events[6].(ToolExecutionEndEvent)
	if toolEnd.Result.ToolCallID != "call_history_mul" {
		t.Fatalf("tool result call id = %q, want call_history_mul", toolEnd.Result.ToolCallID)
	}
	if toolEnd.Result.ToolName != "calculator" {
		t.Fatalf("tool result name = %q, want calculator", toolEnd.Result.ToolName)
	}
	if toolEnd.Result.Content != "80" {
		t.Fatalf("tool result content = %q, want 80", toolEnd.Result.Content)
	}
	if toolEnd.Result.IsError {
		t.Fatal("tool result IsError = true, want false")
	}
	if toolEnd.Error != nil {
		t.Fatalf("tool end error = %v, want nil", toolEnd.Error)
	}

	final := events[9].(MessageEndEvent).Message
	if final.Content != "16 * 5 = 80" {
		t.Fatalf("final assistant content = %q", final.Content)
	}
	if len(final.ToolCalls) != 0 {
		t.Fatalf("final assistant tool calls len = %d, want 0", len(final.ToolCalls))
	}
}

func TestAgentStreamWrapsProviderErrorWithChatRoundContext(t *testing.T) {
	sentinel := errors.New("provider chat stream failed")
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return nil, sentinel
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	events := collectStreamEvents(t, streamAgentText(a, context.Background(), "just answer directly"))
	if len(events) != 3 {
		t.Fatalf("events len = %d, want 3", len(events))
	}
	errEvent, ok := events[2].(ErrorEvent)
	if !ok {
		t.Fatalf("event[2] type = %T, want ErrorEvent", events[2])
	}
	if !strings.Contains(errEvent.Error.Error(), "llm chat round 1") {
		t.Fatalf("stream error = %q, want chat round context", errEvent.Error.Error())
	}
	if !errors.Is(errEvent.Error, sentinel) {
		t.Fatalf("stream error = %v, want to wrap sentinel %v", errEvent.Error, sentinel)
	}
}

func TestAgentStreamReturnsContextCancellationWhenCanceledAfterChat(t *testing.T) {
	oldMaxProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(oldMaxProcs)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		cancel()
		return streamEvents(
			assistantTextDelta("answer dropped by cancellation"),
			assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "answer dropped by cancellation"}),
		), nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	events := collectStreamEvents(t, streamAgentText(a, ctx, "just answer directly"))
	var streamErr error
	messageEnds := 0
	for _, event := range events {
		switch event := event.(type) {
		case ErrorEvent:
			streamErr = event.Error
		case MessageEndEvent:
			messageEnds++
		}
	}
	if !errors.Is(streamErr, context.Canceled) {
		t.Fatalf("stream error = %v, want context cancellation", streamErr)
	}
	if messageEnds != 0 {
		t.Fatalf("message end events = %d, want 0 when cancellation drops terminal assistant message", messageEnds)
	}
}

func TestAgentStreamReturnsCancellationErrorWhenContextAlreadyCanceledWithValidMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		t.Fatal("provider.ChatStream() should not be called for canceled context")
		return nil, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	events := collectStreamEvents(t, a.Stream(ctx, []Message{
		UserMessage{Content: "continue"},
	}))
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	errEvent, ok := events[0].(ErrorEvent)
	if !ok {
		t.Fatalf("event[0] type = %T, want ErrorEvent", events[0])
	}
	if !errors.Is(errEvent.Error, context.Canceled) {
		t.Fatalf("stream error = %v, want context cancellation", errEvent.Error)
	}
}

func TestAgentStreamClosesWithoutEventWhenContextAlreadyCanceledAndHistoryInvalid(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		t.Fatal("provider.ChatStream() should not be called for invalid history")
		return nil, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	stream := a.Stream(ctx, []Message{
		AssistantMessage{},
		UserMessage{Content: "continue"},
	})

	select {
	case event, ok := <-stream:
		if ok {
			t.Fatalf("Stream() delivered %T (%#v), want closed stream without event", event, event)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Stream() did not close promptly")
	}
	if len(provider.requests) != 0 {
		t.Fatalf("provider requests len = %d, want 0", len(provider.requests))
	}
}

func TestAgentStreamRejectsEmptyAssistantResponse(t *testing.T) {
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(assistantDone(llms.Message{Role: llms.RoleAssistant})), nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	events := collectStreamEvents(t, a.Stream(context.Background(), []Message{
		UserMessage{Content: "answer directly"},
	}))
	if len(events) != 4 {
		t.Fatalf("events len = %d, want 4", len(events))
	}
	errEvent, ok := events[3].(ErrorEvent)
	if !ok {
		t.Fatalf("event[3] type = %T, want ErrorEvent", events[3])
	}
	if !strings.Contains(errEvent.Error.Error(), "assistant message must have content or tool calls") {
		t.Fatalf("stream error = %q, want assistant validation", errEvent.Error.Error())
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests len = %d, want 1", len(provider.requests))
	}
}

func TestAgentStreamEmitsToolExecutionEventsAcrossRounds(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		round++
		switch round {
		case 1:
			call := calculatorToolCall("call_step_1", `{"a":2,"b":3,"op":"add"}`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		case 2:
			if got := req.Messages[len(req.Messages)-1]; got.Role != llms.RoleTool || got.Content != "5" {
				t.Fatalf("second request last message = %#v, want tool result 5", got)
			}
			call := calculatorToolCall("call_step_2", `{"a":5,"b":4,"op":"mul"}`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		case 3:
			if got := req.Messages[len(req.Messages)-1]; got.Role != llms.RoleTool || got.Content != "20" {
				t.Fatalf("third request last message = %#v, want tool result 20", got)
			}
			return streamEvents(
				assistantTextDelta("final answer: 20"),
				assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "final answer: 20"}),
			), nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := NewWithOptions(provider, registry, "fake-tool-model", Options{MaxSteps: 2})
	events := collectStreamEvents(t, streamAgentText(a, context.Background(), "compute (2 + 3) * 4"))

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
		ToolExecutionStartEvent{},
		ToolExecutionEndEvent{},
		MessageStartEvent{},
		MessageDeltaEvent{},
		MessageEndEvent{},
		TurnEndEvent{},
		RunEndEvent{},
	)
	if len(provider.requests) != 3 {
		t.Fatalf("chat requests len = %d, want 3", len(provider.requests))
	}

	toolStart1 := events[5].(ToolExecutionStartEvent)
	if toolStart1.ToolCallID != "call_step_1" || toolStart1.ToolName != "calculator" || toolStart1.Arguments != `{"a":2,"b":3,"op":"add"}` {
		t.Fatalf("first tool start = %#v", toolStart1)
	}
	toolEnd1 := events[6].(ToolExecutionEndEvent)
	if toolEnd1.Result.ToolCallID != "call_step_1" || toolEnd1.Result.ToolName != "calculator" || toolEnd1.Result.Content != "5" || toolEnd1.Result.IsError {
		t.Fatalf("first tool end = %#v", toolEnd1)
	}
	if toolEnd1.Error != nil {
		t.Fatalf("first tool end error = %v, want nil", toolEnd1.Error)
	}

	toolStart2 := events[10].(ToolExecutionStartEvent)
	if toolStart2.ToolCallID != "call_step_2" || toolStart2.ToolName != "calculator" || toolStart2.Arguments != `{"a":5,"b":4,"op":"mul"}` {
		t.Fatalf("second tool start = %#v", toolStart2)
	}
	toolEnd2 := events[11].(ToolExecutionEndEvent)
	if toolEnd2.Result.ToolCallID != "call_step_2" || toolEnd2.Result.ToolName != "calculator" || toolEnd2.Result.Content != "20" || toolEnd2.Result.IsError {
		t.Fatalf("second tool end = %#v", toolEnd2)
	}
	if toolEnd2.Error != nil {
		t.Fatalf("second tool end error = %v, want nil", toolEnd2.Error)
	}

	final := events[14].(MessageEndEvent).Message
	if final.Content != "final answer: 20" {
		t.Fatalf("final assistant content = %q", final.Content)
	}
	if len(final.ToolCalls) != 0 {
		t.Fatalf("final assistant tool calls len = %d, want 0", len(final.ToolCalls))
	}
}

func TestAgentStreamContinuesAfterToolErrorAndReturnsFinalAnswer(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		round++
		switch round {
		case 1:
			call := calculatorToolCall("call_bad_args", `{"a":1`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
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
			return streamEvents(
				assistantTextDelta("I couldn't use calculator because the arguments were malformed."),
				assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "I couldn't use calculator because the arguments were malformed."}),
			), nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := New(provider, registry, "fake-tool-model")
	events := collectStreamEvents(t, a.Stream(context.Background(), []Message{
		UserMessage{Content: "try calculator with bad arguments"},
	}))

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
	if len(provider.requests) != 2 {
		t.Fatalf("provider requests len = %d, want 2", len(provider.requests))
	}

	toolEnd := events[6].(ToolExecutionEndEvent)
	if toolEnd.Result.ToolCallID != "call_bad_args" {
		t.Fatalf("tool result call id = %q, want call_bad_args", toolEnd.Result.ToolCallID)
	}
	if toolEnd.Result.ToolName != "calculator" {
		t.Fatalf("tool result name = %q, want calculator", toolEnd.Result.ToolName)
	}
	if !strings.HasPrefix(toolEnd.Result.Content, `tool "calculator" failed: `) {
		t.Fatalf("tool result content = %q, want sanitized failure summary", toolEnd.Result.Content)
	}
	if !toolEnd.Result.IsError {
		t.Fatal("tool result IsError = false, want true")
	}
	if toolEnd.Error == nil || !strings.Contains(toolEnd.Error.Error(), "decode calculator arguments") {
		t.Fatalf("tool end error = %v, want calculator decode failure", toolEnd.Error)
	}

	final := events[9].(MessageEndEvent).Message
	if final.Content != "I couldn't use calculator because the arguments were malformed." {
		t.Fatalf("final assistant content = %q", final.Content)
	}
	if len(final.ToolCalls) != 0 {
		t.Fatalf("final assistant tool calls len = %d, want 0", len(final.ToolCalls))
	}
}

func TestAgentStreamReturnsMaxStepsErrorWhenModelKeepsRetryingAfterToolError(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		round++
		switch round {
		case 1:
			call := calculatorToolCall("call_bad_args", `{"a":1`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		case 2:
			got := req.Messages[len(req.Messages)-1]
			if got.Role != llms.RoleTool || !strings.HasPrefix(got.Content, `tool "calculator" failed: `) {
				t.Fatalf("second request last message = %#v, want sanitized tool failure", got)
			}
			call := calculatorToolCall("call_retry", `{"a":1`)
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := NewWithOptions(provider, registry, "fake-tool-model", Options{MaxSteps: 1})
	events := collectStreamEvents(t, a.Stream(context.Background(), []Message{
		UserMessage{Content: "keep trying even after tool failure"},
	}))

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
		ErrorEvent{},
	)
	if len(provider.requests) != 2 {
		t.Fatalf("provider requests len = %d, want 2", len(provider.requests))
	}

	toolEnd := events[6].(ToolExecutionEndEvent)
	if !toolEnd.Result.IsError {
		t.Fatal("tool result IsError = false, want true")
	}
	if toolEnd.Error == nil || !strings.Contains(toolEnd.Error.Error(), "decode calculator arguments") {
		t.Fatalf("tool end error = %v, want calculator decode failure", toolEnd.Error)
	}

	errEvent := events[9].(ErrorEvent)
	if !strings.Contains(errEvent.Error.Error(), "max steps") {
		t.Fatalf("stream error = %v, want max steps message", errEvent.Error)
	}
}

func TestAgentStreamForwardsSemanticHistoryToProvider(t *testing.T) {
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

	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		assertMessagesEqual(t, req.Messages, want)
		return streamEvents(
			assistantTextDelta("4 * 3 = 12"),
			assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "4 * 3 = 12"}),
		), nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got := collectFinalAnswer(t, a.Stream(context.Background(), history))
	if got != "4 * 3 = 12" {
		t.Fatalf("answer = %q, want %q", got, "4 * 3 = 12")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests len = %d, want 1", len(provider.requests))
	}
}

func TestAgentStreamRejectsInvalidHistoryAndSemanticMessages(t *testing.T) {
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
			provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
				t.Fatal("provider.ChatStream() should not be called for invalid semantic history")
				return nil, nil
			})}

			a := New(provider, tools.NewRegistry(), "fake-tool-model")
			events := collectStreamEvents(t, a.Stream(context.Background(), tt.messages))
			if len(events) != 1 {
				t.Fatalf("events len = %d, want 1", len(events))
			}
			errEvent, ok := events[0].(ErrorEvent)
			if !ok {
				t.Fatalf("event[0] type = %T, want ErrorEvent", events[0])
			}
			if errEvent.Error == nil {
				t.Fatal("stream error = nil, want validation error")
			}
			if len(provider.requests) != 0 {
				t.Fatalf("provider requests len = %d, want 0", len(provider.requests))
			}
		})
	}
}

func TestAgentStreamForwardsProvidedHistoryToProvider(t *testing.T) {
	history := []Message{
		UserMessage{Content: "what is 2 + 2?"},
		AssistantMessage{Content: "2 + 2 = 4."},
		UserMessage{Content: "multiply that by 3"},
	}
	want := []llms.Message{
		{Role: llms.RoleUser, Content: "what is 2 + 2?"},
		{Role: llms.RoleAssistant, Content: "2 + 2 = 4."},
		{Role: llms.RoleUser, Content: "multiply that by 3"},
	}

	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(
			assistantTextDelta("4 * 3 = 12"),
			assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "4 * 3 = 12"}),
		), nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got := collectFinalAnswer(t, a.Stream(context.Background(), history))
	if got != "4 * 3 = 12" {
		t.Fatalf("answer = %q", got)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("chat requests len = %d, want 1", len(provider.requests))
	}

	assertMessagesEqual(t, provider.requests[0].Messages, want)
}
