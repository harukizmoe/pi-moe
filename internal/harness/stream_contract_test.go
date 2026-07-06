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

	if len(events) != 7 {
		t.Fatalf("stream events len = %d, want 7", len(events))
	}

	if _, ok := events[0].(RunStartEvent); !ok {
		t.Fatalf("event[0] = %T, want RunStartEvent", events[0])
	}
	if _, ok := events[1].(LLMRequestEvent); !ok {
		t.Fatalf("event[1] = %T, want LLMRequestEvent", events[1])
	}

	toolCall, ok := events[2].(ToolCallEvent)
	if !ok {
		t.Fatalf("event[2] = %T, want ToolCallEvent", events[2])
	}
	if toolCall.ToolCallID != "call_fake_calculator" {
		t.Fatalf("ToolCallEvent.ToolCallID = %q, want %q", toolCall.ToolCallID, "call_fake_calculator")
	}
	if toolCall.ToolName != "calculator" {
		t.Fatalf("ToolCallEvent.ToolName = %q, want calculator", toolCall.ToolName)
	}
	if toolCall.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("ToolCallEvent.Arguments = %q", toolCall.Arguments)
	}

	toolResult, ok := events[3].(ToolResultEvent)
	if !ok {
		t.Fatalf("event[3] = %T, want ToolResultEvent", events[3])
	}
	if toolResult.ToolCallID != "call_fake_calculator" {
		t.Fatalf("ToolResultEvent.ToolCallID = %q, want %q", toolResult.ToolCallID, "call_fake_calculator")
	}
	if toolResult.ToolName != "calculator" {
		t.Fatalf("ToolResultEvent.ToolName = %q, want calculator", toolResult.ToolName)
	}
	if toolResult.Result != "91" {
		t.Fatalf("ToolResultEvent.Result = %q, want 91", toolResult.Result)
	}
	if toolResult.Error != nil {
		t.Fatalf("ToolResultEvent.Error = %v, want nil", toolResult.Error)
	}

	if _, ok := events[4].(LLMRequestEvent); !ok {
		t.Fatalf("event[4] = %T, want second LLMRequestEvent", events[4])
	}

	final, ok := events[5].(FinalEvent)
	if !ok {
		t.Fatalf("event[5] = %T, want FinalEvent", events[5])
	}
	if final.Answer != "13 * 7 = 91" {
		t.Fatalf("FinalEvent.Answer = %q, want %q", final.Answer, "13 * 7 = 91")
	}

	if _, ok := events[6].(RunEndEvent); !ok {
		t.Fatalf("event[6] = %T, want RunEndEvent", events[6])
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
