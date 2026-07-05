package llms

import (
	"context"
	"testing"
)

func TestFakeProviderReturnsToolCallThenFinalAnswer(t *testing.T) {
	provider, err := NewFakeProvider(ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	first, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "calculate 13 * 7"}},
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}
	if len(first.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d", len(first.Message.ToolCalls))
	}
	if first.Message.ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("tool name = %q", first.Message.ToolCalls[0].Function.Name)
	}

	second, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "calculate 13 * 7"},
			first.Message,
			{Role: "tool", ToolCallID: "call_fake_calculator", Content: "91"},
		},
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if second.Message.Content != "13 * 7 = 91" {
		t.Fatalf("final content = %q", second.Message.Content)
	}
}
