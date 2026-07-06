package session

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func collectSessionStreamEvents(t *testing.T, stream <-chan Event) []Event {
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

	s := newFakeSession(t)

	type streamResult struct {
		event Event
		ok    bool
	}

	for attempt := range 32 {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		stream := s.Prompt(ctx, "use calculator to compute 13 * 7")
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
	s := newFakeSession(t)
	events := collectSessionStreamEvents(t, s.Prompt(context.Background(), "use calculator to compute 13 * 7"))

	assertSessionEventTypes(t, events,
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

	toolCall := events[5].(ToolExecutionStartEvent)
	if toolCall.ToolCallID != "call_fake_calculator" {
		t.Fatalf("ToolExecutionStartEvent.ToolCallID = %q, want %q", toolCall.ToolCallID, "call_fake_calculator")
	}
	if toolCall.ToolName != "calculator" {
		t.Fatalf("ToolExecutionStartEvent.ToolName = %q, want calculator", toolCall.ToolName)
	}
	if toolCall.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("ToolExecutionStartEvent.Arguments = %q", toolCall.Arguments)
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

	finalMessage := events[9].(MessageEndEvent)
	if finalMessage.Message.Content != "13 * 7 = 91" {
		t.Fatalf("final MessageEndEvent.Message.Content = %q, want %q", finalMessage.Message.Content, "13 * 7 = 91")
	}
}
