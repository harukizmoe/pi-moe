package llms

import (
	"context"
	"errors"
	"testing"
)

type streamingProviderStub struct{}

func (streamingProviderStub) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return nil, nil
}

func (streamingProviderStub) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error) {
	return nil, nil
}

var _ StreamingProvider = streamingProviderStub{}

func TestStreamingContract(t *testing.T) {
	t.Run("delta event carries incremental assistant state", func(t *testing.T) {
		event := ChatStreamEvent{
			Type: ChatStreamEventTypeDelta,
			Delta: ChatStreamDelta{
				Role:             RoleAssistant,
				Content:          "Calculating",
				ReasoningContent: "Need the calculator tool.",
				ToolCalls: []ToolCallDelta{{
					Index: 0,
					ID:    "call_1",
					Type:  "function",
					Function: ToolCallFunctionDelta{
						Name:      "calculator",
						Arguments: `{"a":13,"b":7,"op":"mul"}`,
					},
				}},
			},
		}

		if event.Type != ChatStreamEventTypeDelta {
			t.Fatalf("event type = %q", event.Type)
		}
		if event.Delta.Role != RoleAssistant {
			t.Fatalf("delta role = %q", event.Delta.Role)
		}
		if event.Delta.Content != "Calculating" {
			t.Fatalf("delta content = %q", event.Delta.Content)
		}
		if event.Delta.ReasoningContent != "Need the calculator tool." {
			t.Fatalf("delta reasoning = %q", event.Delta.ReasoningContent)
		}
		if len(event.Delta.ToolCalls) != 1 {
			t.Fatalf("tool call deltas len = %d", len(event.Delta.ToolCalls))
		}

		toolCall := event.Delta.ToolCalls[0]
		if toolCall.Index != 0 {
			t.Fatalf("tool call delta index = %d", toolCall.Index)
		}
		if toolCall.ID != "call_1" {
			t.Fatalf("tool call delta id = %q", toolCall.ID)
		}
		if toolCall.Type != "function" {
			t.Fatalf("tool call delta type = %q", toolCall.Type)
		}
		if toolCall.Function.Name != "calculator" {
			t.Fatalf("tool call delta function name = %q", toolCall.Function.Name)
		}
		if toolCall.Function.Arguments != `{"a":13,"b":7,"op":"mul"}` {
			t.Fatalf("tool call delta arguments = %q", toolCall.Function.Arguments)
		}
	})

	t.Run("done event carries final assistant message", func(t *testing.T) {
		event := ChatStreamEvent{
			Type: ChatStreamEventTypeDone,
			Message: Message{
				Role:    RoleAssistant,
				Content: "13 * 7 = 91",
			},
		}

		if event.Type != ChatStreamEventTypeDone {
			t.Fatalf("event type = %q", event.Type)
		}
		if event.Message.Role != RoleAssistant {
			t.Fatalf("done role = %q", event.Message.Role)
		}
		if event.Message.Content != "13 * 7 = 91" {
			t.Fatalf("done content = %q", event.Message.Content)
		}
	})

	t.Run("error event carries provider error", func(t *testing.T) {
		sentinel := errors.New("stream interrupted")
		event := ChatStreamEvent{
			Type: ChatStreamEventTypeError,
			Err:  sentinel,
		}

		if event.Type != ChatStreamEventTypeError {
			t.Fatalf("event type = %q", event.Type)
		}
		if !errors.Is(event.Err, sentinel) {
			t.Fatalf("error event err = %v", event.Err)
		}
	})
}
