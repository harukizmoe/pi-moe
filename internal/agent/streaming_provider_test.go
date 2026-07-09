package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

type streamingProviderStub struct {
	streamFunc func(context.Context, llms.ChatRequest) (<-chan llms.ChatStreamEvent, error)

	streamCalls int
	requests    []llms.ChatRequest
}

func (p *streamingProviderStub) ChatStream(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
	p.streamCalls++
	p.requests = append(p.requests, cloneChatRequest(req))
	if p.streamFunc != nil {
		return p.streamFunc(ctx, req)
	}
	return nil, errors.New("missing ChatStream stub")
}

func cloneChatRequest(req llms.ChatRequest) llms.ChatRequest {
	clone := req
	clone.Messages = append([]llms.Message(nil), req.Messages...)
	for i := range clone.Messages {
		clone.Messages[i].ToolCalls = append([]llms.ToolCall(nil), clone.Messages[i].ToolCalls...)
	}
	clone.Tools = append([]llms.Tool(nil), req.Tools...)
	return clone
}

func streamEvents(events ...llms.ChatStreamEvent) <-chan llms.ChatStreamEvent {
	stream := make(chan llms.ChatStreamEvent, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return stream
}

func TestAgentStreamUsesChatStream(t *testing.T) {
	provider := &streamingProviderStub{
		streamFunc: func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
			return streamEvents(
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Role: llms.RoleAssistant, Content: "streamed "}},
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Content: "answer"}},
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDone, Message: llms.Message{Role: llms.RoleAssistant, Content: "streamed answer"}},
			), nil
		},
	}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	events := collectStreamEvents(t, streamAgentText(a, context.Background(), "just answer directly"))

	assertEventTypes(t, events,
		RunStartEvent{},
		TurnStartEvent{},
		MessageStartEvent{},
		MessageDeltaEvent{},
		MessageDeltaEvent{},
		MessageEndEvent{},
		TurnEndEvent{},
		RunEndEvent{},
	)

	if provider.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", provider.streamCalls)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("requests len = %d, want 1", len(provider.requests))
	}

	assertMessagesEqual(t, provider.requests[0].Messages, []llms.Message{{Role: llms.RoleUser, Content: "just answer directly"}})

	firstDelta := events[3].(MessageDeltaEvent)
	if firstDelta.Kind != MessageDeltaText {
		t.Fatalf("first delta kind = %q, want %q", firstDelta.Kind, MessageDeltaText)
	}
	if firstDelta.Delta != "streamed " {
		t.Fatalf("first delta = %q, want %q", firstDelta.Delta, "streamed ")
	}

	secondDelta := events[4].(MessageDeltaEvent)
	if secondDelta.Kind != MessageDeltaText {
		t.Fatalf("second delta kind = %q, want %q", secondDelta.Kind, MessageDeltaText)
	}
	if secondDelta.Delta != "answer" {
		t.Fatalf("second delta = %q, want %q", secondDelta.Delta, "answer")
	}

	final := events[5].(MessageEndEvent)
	if final.Message.Content != "streamed answer" {
		t.Fatalf("final message content = %q, want %q", final.Message.Content, "streamed answer")
	}
	if len(final.Message.ToolCalls) != 0 {
		t.Fatalf("final tool calls len = %d, want 0", len(final.Message.ToolCalls))
	}
}

func TestAgentStreamStreamingToolCallsContinueWithToolResult(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	toolCall := calculatorToolCall("call_stream_calc", `{"a":13,"b":7,"op":"mul"}`)
	provider := &streamingProviderStub{}
	provider.streamFunc = func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		switch provider.streamCalls {
		case 1:
			return streamEvents(
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCallDelta{{Index: 0, ID: toolCall.ID, Type: toolCall.Type, Function: llms.ToolCallFunctionDelta{Name: toolCall.Function.Name, Arguments: `{"a":13`}}}}},
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{ToolCalls: []llms.ToolCallDelta{{Index: 0, Function: llms.ToolCallFunctionDelta{Arguments: `,"b":7`}}}}},
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{ToolCalls: []llms.ToolCallDelta{{Index: 0, Function: llms.ToolCallFunctionDelta{Arguments: `,"op":"mul"}`}}}}},
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDone, Message: llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{toolCall}}},
			), nil
		case 2:
			return streamEvents(
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Role: llms.RoleAssistant, Content: "13 * 7 = 91"}},
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDone, Message: llms.Message{Role: llms.RoleAssistant, Content: "13 * 7 = 91"}},
			), nil
		default:
			t.Fatalf("unexpected ChatStream round = %d", provider.streamCalls)
			return nil, nil
		}
	}

	a := New(provider, registry, "fake-tool-model")
	events := collectStreamEvents(t, a.Stream(context.Background(), []Message{UserMessage{Content: "use calculator to compute 13 * 7"}}))

	assertEventTypes(t, events,
		RunStartEvent{},
		TurnStartEvent{},
		MessageStartEvent{},
		MessageDeltaEvent{},
		MessageDeltaEvent{},
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

	if provider.streamCalls != 2 {
		t.Fatalf("stream calls = %d, want 2", provider.streamCalls)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("requests len = %d, want 2", len(provider.requests))
	}

	assertMessagesEqual(t, provider.requests[0].Messages, []llms.Message{{Role: llms.RoleUser, Content: "use calculator to compute 13 * 7"}})
	assertMessagesEqual(t, provider.requests[1].Messages, []llms.Message{
		{Role: llms.RoleUser, Content: "use calculator to compute 13 * 7"},
		{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{toolCall}},
		{Role: llms.RoleTool, ToolCallID: toolCall.ID, Content: "91"},
	})

	for i, want := range []string{`{"a":13`, `,"b":7`, `,"op":"mul"}`} {
		delta := events[3+i].(MessageDeltaEvent)
		if delta.Kind != MessageDeltaToolCall {
			t.Fatalf("tool delta %d kind = %q, want %q", i, delta.Kind, MessageDeltaToolCall)
		}
		if delta.Delta != want {
			t.Fatalf("tool delta %d = %q, want %q", i, delta.Delta, want)
		}
	}

	firstAssistant := events[6].(MessageEndEvent)
	if len(firstAssistant.Message.ToolCalls) != 1 {
		t.Fatalf("first assistant tool calls len = %d, want 1", len(firstAssistant.Message.ToolCalls))
	}
	if got := firstAssistant.Message.ToolCalls[0]; got != toolCall {
		t.Fatalf("first assistant tool call = %#v, want %#v", got, toolCall)
	}

	toolStart := events[7].(ToolExecutionStartEvent)
	if toolStart.ToolCallID != toolCall.ID || toolStart.ToolName != toolCall.Function.Name || toolStart.Arguments != toolCall.Function.Arguments {
		t.Fatalf("tool start = %#v", toolStart)
	}

	toolEnd := events[8].(ToolExecutionEndEvent)
	if toolEnd.ToolCallID != toolCall.ID {
		t.Fatalf("tool end call id = %q, want %q", toolEnd.ToolCallID, toolCall.ID)
	}
	if toolEnd.Result.ToolCallID != toolCall.ID || toolEnd.Result.ToolName != toolCall.Function.Name || toolEnd.Result.Content != "91" || toolEnd.Result.IsError {
		t.Fatalf("tool end result = %#v", toolEnd.Result)
	}
	if toolEnd.Error != nil {
		t.Fatalf("tool end error = %v, want nil", toolEnd.Error)
	}

	final := events[11].(MessageEndEvent)
	if final.Message.Content != "13 * 7 = 91" {
		t.Fatalf("final message content = %q, want %q", final.Message.Content, "13 * 7 = 91")
	}
}

func TestAgentStreamReturnsChatStreamError(t *testing.T) {
	tests := []struct {
		name        string
		synchronous bool
		streamErr   error
	}{
		{name: "chat_stream_call_error", synchronous: true, streamErr: errors.New("stream setup failed")},
		{name: "chat_stream_error_event", streamErr: errors.New("stream delta failed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &streamingProviderStub{
				streamFunc: func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
					if tt.synchronous {
						return nil, tt.streamErr
					}
					return streamEvents(llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeError, Err: tt.streamErr}), nil
				},
			}

			a := New(provider, tools.NewRegistry(), "fake-tool-model")
			events := collectStreamEvents(t, streamAgentText(a, context.Background(), "just answer directly"))

			if provider.streamCalls != 1 {
				t.Fatalf("stream calls = %d, want 1", provider.streamCalls)
			}
			if len(provider.requests) != 1 {
				t.Fatalf("requests len = %d, want 1", len(provider.requests))
			}

			assertMessagesEqual(t, provider.requests[0].Messages, []llms.Message{{Role: llms.RoleUser, Content: "just answer directly"}})

			var errEvent *ErrorEvent
			for _, event := range events {
				switch event := event.(type) {
				case ErrorEvent:
					eventCopy := event
					errEvent = &eventCopy
				case RunEndEvent:
					t.Fatalf("unexpected RunEndEvent: %#v", event)
				}
			}
			if errEvent == nil {
				t.Fatalf("events = %#v, want ErrorEvent", events)
			}
			if !strings.Contains(errEvent.Error.Error(), "llm chat round 1") {
				t.Fatalf("stream error = %q, want llm chat round context", errEvent.Error.Error())
			}
			if !errors.Is(errEvent.Error, tt.streamErr) {
				t.Fatalf("stream error = %v, want to wrap %v", errEvent.Error, tt.streamErr)
			}
		})
	}
}

func TestAgentStreamMaxStepsOverLimitDoesNotEmitMessageEndEvent(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	firstToolCall := calculatorToolCall("call_step_1", `{"a":13,"b":7,"op":"mul"}`)
	overLimitToolCall := calculatorToolCall("call_step_2", `{"a":1,"b":2,"op":"add"}`)
	provider := &streamingProviderStub{}
	provider.streamFunc = func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		switch provider.streamCalls {
		case 1:
			return streamEvents(
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCallDelta{{Index: 0, ID: firstToolCall.ID, Type: firstToolCall.Type, Function: llms.ToolCallFunctionDelta{Name: firstToolCall.Function.Name, Arguments: firstToolCall.Function.Arguments}}}}},
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDone, Message: llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{firstToolCall}}},
			), nil
		case 2:
			return streamEvents(
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCallDelta{{Index: 0, ID: overLimitToolCall.ID, Type: overLimitToolCall.Type, Function: llms.ToolCallFunctionDelta{Name: overLimitToolCall.Function.Name, Arguments: overLimitToolCall.Function.Arguments}}}}},
				llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDone, Message: llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{overLimitToolCall}}},
			), nil
		default:
			t.Fatalf("unexpected ChatStream round = %d", provider.streamCalls)
			return nil, nil
		}
	}

	a := NewWithOptions(provider, registry, "fake-tool-model", Options{MaxSteps: 1})
	events := collectStreamEvents(t, streamAgentText(a, context.Background(), "keep calling tools"))

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

	messageEnds := 0
	for _, event := range events {
		if _, ok := event.(MessageEndEvent); ok {
			messageEnds++
		}
	}
	if messageEnds != 1 {
		t.Fatalf("message end events = %d, want 1 before over-limit assistant is dropped", messageEnds)
	}

	errEvent := events[len(events)-1].(ErrorEvent)
	if !strings.Contains(errEvent.Error.Error(), "max steps") {
		t.Fatalf("stream error = %v, want max steps context", errEvent.Error)
	}
}

func TestAgentStreamPreservesWhitespaceToolCallDeltaAndRejectsBlankArguments(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	blankArgsCall := calculatorToolCall("call_blank_args", " ")
	provider := &streamingProviderStub{}
	provider.streamFunc = func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		if provider.streamCalls != 1 {
			t.Fatalf("unexpected ChatStream round = %d", provider.streamCalls)
			return nil, nil
		}
		return streamEvents(
			llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCallDelta{{Index: 0, ID: blankArgsCall.ID, Type: blankArgsCall.Type, Function: llms.ToolCallFunctionDelta{Name: blankArgsCall.Function.Name, Arguments: " "}}}}},
			llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDone, Message: llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{blankArgsCall}}},
		), nil
	}

	a := New(provider, registry, "fake-tool-model")
	events := collectStreamEvents(t, streamAgentText(a, context.Background(), "call calculator with blank args"))

	assertEventTypes(t, events,
		RunStartEvent{},
		TurnStartEvent{},
		MessageStartEvent{},
		MessageDeltaEvent{},
		ErrorEvent{},
	)

	toolDelta := events[3].(MessageDeltaEvent)
	if toolDelta.Kind != MessageDeltaToolCall {
		t.Fatalf("tool delta kind = %q, want %q", toolDelta.Kind, MessageDeltaToolCall)
	}
	if toolDelta.Delta != " " {
		t.Fatalf("tool delta = %q, want single-space arguments", toolDelta.Delta)
	}

	errEvent := events[4].(ErrorEvent)
	if errEvent.Error == nil || !strings.Contains(errEvent.Error.Error(), "tool call arguments") {
		t.Fatalf("stream error = %v, want tool call argument validation", errEvent.Error)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests len = %d, want one failed validation round", len(provider.requests))
	}
}
