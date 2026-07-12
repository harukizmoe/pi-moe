package agent

import (
	"context"
	"errors"
	"testing"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

func TestRuntimeRunEmitsCompletedTerminal(t *testing.T) {
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(
			assistantTextDelta("done"),
			assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done"}),
		), nil
	})
	runtime := NewRuntime(provider, tools.NewRegistry(), "fake-model", Options{})

	events := collectStreamEvents(t, runtime.Run(context.Background(), RunRequest{Messages: []Message{UserMessage{Content: "answer"}}}))
	if len(events) == 0 {
		t.Fatal("events empty")
	}
	terminalCount := 0
	for _, event := range events {
		switch event := event.(type) {
		case RunCompletedEvent:
			terminalCount++
			if event.RunID == "" {
				t.Fatal("completed RunID is empty")
			}
		case RunFailedEvent, RunCanceledEvent:
			terminalCount++
		}
		if _, ok := event.(RunEndEvent); ok {
			t.Fatalf("legacy success terminal leaked into Runtime stream: %#v", event)
		}
		if _, ok := event.(ErrorEvent); ok {
			t.Fatalf("legacy error terminal leaked into Runtime stream: %#v", event)
		}
	}
	if terminalCount != 1 {
		t.Fatalf("terminal events = %d, want exactly one: %#v", terminalCount, events)
	}
	if _, ok := events[len(events)-1].(RunCompletedEvent); !ok {
		t.Fatalf("last event = %T, want RunCompletedEvent", events[len(events)-1])
	}
}

func TestRuntimeRunMapsProviderErrorToFailedTerminal(t *testing.T) {
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return nil, errors.New("provider unavailable")
	})
	runtime := NewRuntime(provider, tools.NewRegistry(), "fake-model", Options{})

	events := collectStreamEvents(t, runtime.Run(context.Background(), RunRequest{Messages: []Message{UserMessage{Content: "answer"}}}))
	failed, ok := events[len(events)-1].(RunFailedEvent)
	if !ok {
		t.Fatalf("last event = %T, want RunFailedEvent", events[len(events)-1])
	}
	if failed.Error == nil || failed.Error.Error() == "" {
		t.Fatalf("failed error = %v, want provider error", failed.Error)
	}
}

func TestRuntimeRunMapsCancellationToCanceledTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		t.Fatal("provider called after cancellation")
		return nil, nil
	})
	runtime := NewRuntime(provider, tools.NewRegistry(), "fake-model", Options{})

	events := collectStreamEvents(t, runtime.Run(ctx, RunRequest{Messages: []Message{UserMessage{Content: "answer"}}}))
	canceled, ok := events[len(events)-1].(RunCanceledEvent)
	if !ok {
		t.Fatalf("last event = %T, want RunCanceledEvent", events[len(events)-1])
	}
	if !errors.Is(canceled.Error, context.Canceled) {
		t.Fatalf("canceled error = %v, want context.Canceled", canceled.Error)
	}
}

func TestRuntimeRunSnapshotsRequestMessages(t *testing.T) {
	var got string
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		got = req.Messages[len(req.Messages)-1].Content
		return streamEvents(assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done"})), nil
	})
	runtime := NewRuntime(provider, tools.NewRegistry(), "fake-model", Options{})
	messages := []Message{UserMessage{Content: "original"}}
	request := NewRunRequest(messages)
	messages[0] = UserMessage{Content: "mutated after request"}

	collectStreamEvents(t, runtime.Run(context.Background(), request))
	if got != "original" {
		t.Fatalf("provider saw %q, want request snapshot %q", got, "original")
	}
}
