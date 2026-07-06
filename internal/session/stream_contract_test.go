package session

import (
	"context"
	"errors"
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

func TestSessionPromptWithAlreadyCanceledContextEmitsCancellationErrorAndLeavesTranscriptEmpty(t *testing.T) {
	oldMaxProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(oldMaxProcs)
	})

	s := newFakeSession(t)

	for attempt := range 32 {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		stream := s.Prompt(ctx, "use calculator to compute 13 * 7")
		resultCh := make(chan []Event, 1)
		go func() {
			resultCh <- collectSessionStreamEvents(t, stream)
		}()

		runtime.Gosched()

		select {
		case events := <-resultCh:
			if len(events) != 1 {
				t.Fatalf("attempt %d: Prompt() events len = %d, want 1", attempt, len(events))
			}
			errEvent, ok := events[0].(ErrorEvent)
			if !ok {
				t.Fatalf("attempt %d: Prompt() event[0] = %T, want ErrorEvent", attempt, events[0])
			}
			if !errors.Is(errEvent.Error, context.Canceled) {
				t.Fatalf("attempt %d: Prompt() ErrorEvent.Error = %v, want context cancellation", attempt, errEvent.Error)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Prompt() did not close promptly")
		}

		if messages := s.Messages(); len(messages) != 0 {
			t.Fatalf("attempt %d: Messages() len after canceled Prompt = %d, want 0", attempt, len(messages))
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
