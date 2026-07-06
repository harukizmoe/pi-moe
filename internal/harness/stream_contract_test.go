package harness

import (
	"context"
	"reflect"
	"runtime"
	"testing"
	"time"
)

type harnessStreamer interface {
	Stream(context.Context, string) <-chan Event
}

var _ harnessStreamer = (*Harness)(nil)

func collectHarnessStreamEvents(t *testing.T, stream <-chan Event) []Event {
	t.Helper()

	var events []Event
	for event := range stream {
		events = append(events, event)
	}

	return events
}

func TestStreamClosesWithoutEventWhenContextAlreadyCanceledAndInputEmpty(t *testing.T) {
	oldMaxProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(oldMaxProcs)
	})

	h := newFakeHarness(t)

	type streamResult struct {
		event Event
		ok    bool
	}

	for attempt := 0; attempt < 32; attempt++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		stream := h.Stream(ctx, "")
		resultCh := make(chan streamResult, 1)
		go func() {
			event, ok := <-stream
			resultCh <- streamResult{event: event, ok: ok}
		}()

		runtime.Gosched()

		select {
		case result := <-resultCh:
			if result.ok {
				t.Fatalf("attempt %d: Stream() delivered %T (%#v), want closed stream without event", attempt, result.event, result.event)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Stream() did not close promptly")
		}
	}
}

func TestStreamReturnsTypedToolCallingEvents(t *testing.T) {
	h := newFakeHarness(t)
	events := collectHarnessStreamEvents(t, h.Stream(context.Background(), "use calculator to compute 13 * 7"))

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

func TestRunReturnsToolTraceWithoutEventHistory(t *testing.T) {
	h := newFakeHarness(t)

	got, err := h.Run(context.Background(), "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got == nil {
		t.Fatal("Run() result = nil")
	}
	if got.Answer != "13 * 7 = 91" {
		t.Fatalf("Run().Answer = %q, want %q", got.Answer, "13 * 7 = 91")
	}
	if got.ToolRounds != 1 {
		t.Fatalf("Run().ToolRounds = %d, want 1", got.ToolRounds)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("Run().Steps len = %d, want 1", len(got.Steps))
	}

	step := got.Steps[0]
	if step.ToolName != "calculator" {
		t.Fatalf("Run().Steps[0].ToolName = %q, want calculator", step.ToolName)
	}
	if step.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("Run().Steps[0].Arguments = %q", step.Arguments)
	}
	if step.Result != "91" {
		t.Fatalf("Run().Steps[0].Result = %q, want 91", step.Result)
	}
	if step.Error != "" {
		t.Fatalf("Run().Steps[0].Error = %q, want empty", step.Error)
	}

	if _, ok := reflect.TypeOf(*got).FieldByName("Events"); ok {
		t.Fatalf("Run() result exposes deprecated Events field")
	}
}
