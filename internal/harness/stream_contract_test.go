package harness

import (
	"context"
	"runtime"
	"testing"
	"time"
)


func collectHarnessStreamEvents(t *testing.T, stream <-chan Event) []Event {
	t.Helper()

	var events []Event
	for event := range stream {
		events = append(events, event)
	}

	return events
}

func TestSessionPromptClosesWithoutEventWhenContextAlreadyCanceled(t *testing.T) {
	oldMaxProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(oldMaxProcs)
	})

	h := newFakeHarness(t)

	type streamResult struct {
		event Event
		ok    bool
	}

	for attempt := range 32 {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		session := h.NewSession()
		stream := session.Prompt(ctx, "use calculator to compute 13 * 7")
		resultCh := make(chan streamResult, 1)
		go func() {
			event, ok := <-stream
			resultCh <- streamResult{event: event, ok: ok}
		}()

		runtime.Gosched()

		select {
		case result := <-resultCh:
			if result.ok {
				t.Fatalf("attempt %d: Prompt() delivered %T (%#v), want closed stream without event", attempt, result.event, result.event)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Prompt() did not close promptly")
		}
	}
}

func TestSessionPromptReturnsTypedToolCallingEvents(t *testing.T) {
	h := newFakeHarness(t)
	events := collectHarnessStreamEvents(t, h.NewSession().Prompt(context.Background(), "use calculator to compute 13 * 7"))

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

	turnStart := events[1].(TurnStartEvent)
	if turnStart.Turn != 1 {
		t.Fatalf("TurnStartEvent.Turn = %d, want 1", turnStart.Turn)
	}
	if turnStart.UserMessage.Content != "use calculator to compute 13 * 7" {
		t.Fatalf("TurnStartEvent.UserMessage.Content = %q", turnStart.UserMessage.Content)
	}

	toolCallDelta := events[3].(MessageDeltaEvent)
	if toolCallDelta.Kind != MessageDeltaToolCall {
		t.Fatalf("tool-call delta kind = %q, want %q", toolCallDelta.Kind, MessageDeltaToolCall)
	}
	if toolCallDelta.Delta != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("tool-call delta = %q", toolCallDelta.Delta)
	}

	assistantWithTool := events[4].(MessageEndEvent).Message
	if len(assistantWithTool.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls len = %d, want 1", len(assistantWithTool.ToolCalls))
	}
	if assistantWithTool.ToolCalls[0].ID != "call_fake_calculator" {
		t.Fatalf("assistant tool call id = %q, want %q", assistantWithTool.ToolCalls[0].ID, "call_fake_calculator")
	}

	toolStart := events[5].(ToolExecutionStartEvent)
	if toolStart.ToolCallID != "call_fake_calculator" {
		t.Fatalf("ToolExecutionStartEvent.ToolCallID = %q, want %q", toolStart.ToolCallID, "call_fake_calculator")
	}
	if toolStart.ToolName != "calculator" {
		t.Fatalf("ToolExecutionStartEvent.ToolName = %q, want calculator", toolStart.ToolName)
	}
	if toolStart.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("ToolExecutionStartEvent.Arguments = %q", toolStart.Arguments)
	}

	toolResult := events[6].(ToolExecutionEndEvent)
	if toolResult.ToolCallID != "call_fake_calculator" {
		t.Fatalf("ToolExecutionEndEvent.ToolCallID = %q, want %q", toolResult.ToolCallID, "call_fake_calculator")
	}
	if toolResult.Result.ToolName != "calculator" {
		t.Fatalf("ToolExecutionEndEvent.Result.ToolName = %q, want calculator", toolResult.Result.ToolName)
	}
	if toolResult.Result.Content != "91" {
		t.Fatalf("ToolExecutionEndEvent.Result.Content = %q, want 91", toolResult.Result.Content)
	}
	if toolResult.Error != nil {
		t.Fatalf("ToolExecutionEndEvent.Error = %v, want nil", toolResult.Error)
	}

	finalDelta := events[8].(MessageDeltaEvent)
	if finalDelta.Kind != MessageDeltaText {
		t.Fatalf("final delta kind = %q, want %q", finalDelta.Kind, MessageDeltaText)
	}
	if finalDelta.Delta != "13 * 7 = 91" {
		t.Fatalf("final delta = %q, want %q", finalDelta.Delta, "13 * 7 = 91")
	}

	final := events[9].(MessageEndEvent)
	if final.Message.Content != "13 * 7 = 91" {
		t.Fatalf("MessageEndEvent.Message.Content = %q, want %q", final.Message.Content, "13 * 7 = 91")
	}

	turnEnd := events[10].(TurnEndEvent)
	if turnEnd.Turn != 1 {
		t.Fatalf("TurnEndEvent.Turn = %d, want 1", turnEnd.Turn)
	}
}

