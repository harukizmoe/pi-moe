package agent

import (
	"context"
	"reflect"
	"testing"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

func TestAgentStreamEmitsNoToolMessageLifecycle(t *testing.T) {
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(
			assistantTextDelta("done without tools"),
			assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done without tools"}),
		), nil
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
	if toolStart.ArgumentsDigest != digestString(`{"a":13,"b":7,"op":"mul"}`) {
		t.Fatalf("ToolExecutionStartEvent leaked arguments or wrong digest: %#v", toolStart)
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
