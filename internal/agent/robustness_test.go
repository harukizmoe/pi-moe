package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

func TestNewWithOptionsTreatsNilToolsRegistryAsEmpty(t *testing.T) {
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(
			assistantTextDelta("done without tools"),
			assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done without tools"}),
		), nil
	})}

	a := NewWithOptions(provider, nil, "fake-model", Options{})
	events := collectStreamEvents(t, a.Stream(context.Background(), []Message{UserMessage{Content: "answer directly"}}))

	assertEventTypes(t, events,
		RunStartEvent{},
		TurnStartEvent{},
		MessageStartEvent{},
		MessageDeltaEvent{},
		MessageEndEvent{},
		TurnEndEvent{},
		RunEndEvent{},
	)
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests len = %d, want 1", len(provider.requests))
	}
	if len(provider.requests[0].Tools) != 0 {
		t.Fatalf("provider tools len = %d, want empty registry", len(provider.requests[0].Tools))
	}
}

func TestEmitCancellationKeepsTerminalEventWhenStreamIsFull(t *testing.T) {
	stream := make(chan Event, 1)
	stream <- MessageDeltaEvent{Delta: "buffered non-terminal event"}

	cancelErr := ErrorEvent{Error: context.Canceled}
	emitCancellation(stream, cancelErr)

	got := <-stream
	errEvent, ok := got.(ErrorEvent)
	if !ok {
		t.Fatalf("buffered event = %T, want ErrorEvent", got)
	}
	if !errors.Is(errEvent.Error, context.Canceled) {
		t.Fatalf("ErrorEvent.Error = %v, want context cancellation", errEvent.Error)
	}
}

func TestAgentStreamRejectsProviderDoneWithNonAssistantRole(t *testing.T) {
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(assistantDone(llms.Message{Role: llms.RoleUser, Content: "not an assistant message"})), nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-model")
	events := collectStreamEvents(t, a.Stream(context.Background(), []Message{UserMessage{Content: "answer directly"}}))

	if len(provider.requests) != 1 {
		t.Fatalf("provider requests len = %d, want 1", len(provider.requests))
	}
	if len(events) == 0 {
		t.Fatal("events empty, want ErrorEvent")
	}
	last := events[len(events)-1]
	errEvent, ok := last.(ErrorEvent)
	if !ok {
		t.Fatalf("last event = %T, want ErrorEvent; events=%#v", last, events)
	}
	if !strings.Contains(errEvent.Error.Error(), "assistant response role") {
		t.Fatalf("stream error = %q, want assistant response role validation", errEvent.Error.Error())
	}
	for _, event := range events {
		if _, ok := event.(RunEndEvent); ok {
			t.Fatalf("unexpected RunEndEvent after invalid provider role: %#v", events)
		}
	}
}

func TestAgentStreamRejectsMalformedToolCallArgumentsBeforeExecution(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})
	badCall := calculatorToolCall("call_bad_json", `{"a":1`)
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(
			assistantToolCallDelta(0, badCall),
			assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{badCall}}),
		), nil
	})}

	a := New(provider, registry, "fake-model")
	events := collectStreamEvents(t, a.Stream(context.Background(), []Message{UserMessage{Content: "call calculator"}}))

	if len(provider.requests) != 1 {
		t.Fatalf("provider requests len = %d, want one failed boundary validation round", len(provider.requests))
	}
	var errEvent *ErrorEvent
	for _, event := range events {
		switch event := event.(type) {
		case ToolExecutionStartEvent:
			t.Fatalf("unexpected ToolExecutionStartEvent for malformed arguments: %#v", event)
		case ErrorEvent:
			copy := event
			errEvent = &copy
		}
	}
	if errEvent == nil {
		t.Fatalf("events = %#v, want ErrorEvent", events)
	}
	if !strings.Contains(errEvent.Error.Error(), "tool call arguments") {
		t.Fatalf("stream error = %q, want tool call argument validation", errEvent.Error.Error())
	}
}

func TestToolErrorContentIsSafeWhileEventKeepsInternalError(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	badCall := calculatorToolCall("call_divide_by_zero", `{"a":1,"b":0,"op":"div"}`)
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		switch len(req.Messages) {
		case 1:
			return streamEvents(
				assistantToolCallDelta(0, badCall),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{badCall}}),
			), nil
		case 3:
			toolResult := req.Messages[2]
			if toolResult.Role != llms.RoleTool {
				t.Fatalf("second request tool result role = %q, want tool", toolResult.Role)
			}
			if toolResult.Content != `tool "calculator" failed` {
				t.Fatalf("second request tool content = %q, want safe summary", toolResult.Content)
			}
			return streamEvents(
				assistantTextDelta("handled tool failure"),
				assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "handled tool failure"}),
			), nil
		default:
			t.Fatalf("unexpected provider request messages len = %d", len(req.Messages))
			return nil, nil
		}
	})}

	a := New(provider, registry, "fake-model")
	events := collectStreamEvents(t, a.Stream(context.Background(), []Message{UserMessage{Content: "divide by zero"}}))

	var toolEnd *ToolExecutionEndEvent
	for _, event := range events {
		if event, ok := event.(ToolExecutionEndEvent); ok {
			copy := event
			toolEnd = &copy
		}
	}
	if toolEnd == nil {
		t.Fatalf("events = %#v, want ToolExecutionEndEvent", events)
	}
	if toolEnd.Error == nil || !strings.Contains(toolEnd.Error.Error(), "divide by zero") {
		t.Fatalf("tool end error = %v, want internal divide-by-zero error", toolEnd.Error)
	}
	if toolEnd.Result.Content != `tool "calculator" failed` {
		t.Fatalf("tool result content = %q, want safe summary", toolEnd.Result.Content)
	}
	if strings.Contains(toolEnd.Result.Content, "divide by zero") {
		t.Fatalf("tool result content = %q, leaked internal error", toolEnd.Result.Content)
	}
	if !toolEnd.Result.IsError {
		t.Fatal("tool result IsError = false, want true")
	}
}
