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

	first, err := CollectChat(context.Background(), provider, ChatRequest{
		Messages: []Message{{Role: "user", Content: "calculate 13 * 7"}},
	})
	if err != nil {
		t.Fatalf("first CollectChat() error = %v", err)
	}
	if len(first.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d", len(first.Message.ToolCalls))
	}
	if first.Message.ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("tool name = %q", first.Message.ToolCalls[0].Function.Name)
	}

	second, err := CollectChat(context.Background(), provider, ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "calculate 13 * 7"},
			first.Message,
			{Role: "tool", ToolCallID: "call_fake_calculator", Content: "91"},
		},
	})
	if err != nil {
		t.Fatalf("second CollectChat() error = %v", err)
	}
	if second.Message.Content != "13 * 7 = 91" {
		t.Fatalf("final content = %q", second.Message.Content)
	}
}

func TestFakeProviderChatStreamReturnsToolCallThenFinalAnswer(t *testing.T) {
	provider, err := NewFakeProvider(ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	firstStream, err := provider.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "calculate 13 * 7"}},
	})
	if err != nil {
		t.Fatalf("first ChatStream() error = %v", err)
	}

	firstEvents := collectChatStreamEvents(t, firstStream)
	var firstToolArguments string
	var firstDone *ChatStreamEvent
	for _, event := range firstEvents {
		switch event.Type {
		case ChatStreamEventTypeDelta:
			for _, toolCall := range event.Delta.ToolCalls {
				firstToolArguments += toolCall.Function.Arguments
			}
		case ChatStreamEventTypeDone:
			eventCopy := event
			firstDone = &eventCopy
		case ChatStreamEventTypeError:
			t.Fatalf("unexpected first-round error event: %v", event.Err)
		}
	}

	if firstDone == nil {
		t.Fatal("missing first-round done event")
	}
	if firstDone.Message.Role != RoleAssistant {
		t.Fatalf("first-round role = %q", firstDone.Message.Role)
	}
	if len(firstDone.Message.ToolCalls) != 1 {
		t.Fatalf("first-round tool calls len = %d", len(firstDone.Message.ToolCalls))
	}
	if firstDone.Message.ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("first-round tool name = %q", firstDone.Message.ToolCalls[0].Function.Name)
	}
	if firstToolArguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("first-round tool call delta arguments = %q", firstToolArguments)
	}

	secondStream, err := provider.ChatStream(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: RoleUser, Content: "calculate 13 * 7"},
			firstDone.Message,
			{Role: RoleTool, ToolCallID: "call_fake_calculator", Content: "91"},
		},
	})
	if err != nil {
		t.Fatalf("second ChatStream() error = %v", err)
	}

	secondEvents := collectChatStreamEvents(t, secondStream)
	var secondContent string
	var secondDone *ChatStreamEvent
	for _, event := range secondEvents {
		switch event.Type {
		case ChatStreamEventTypeDelta:
			secondContent += event.Delta.Content
		case ChatStreamEventTypeDone:
			eventCopy := event
			secondDone = &eventCopy
		case ChatStreamEventTypeError:
			t.Fatalf("unexpected second-round error event: %v", event.Err)
		}
	}

	if secondContent != "13 * 7 = 91" {
		t.Fatalf("second-round delta content = %q", secondContent)
	}
	if secondDone == nil {
		t.Fatal("missing second-round done event")
	}
	if secondDone.Message.Content != "13 * 7 = 91" {
		t.Fatalf("second-round done content = %q", secondDone.Message.Content)
	}
}

func TestFakeProviderAnswersPreviousResultFromToolHistory(t *testing.T) {
	for _, prompt := range []string{"what was the previous result?", "上一轮结果是什么？"} {
		t.Run(prompt, func(t *testing.T) {
			provider := &FakeProvider{model: "fake-tool-model"}
			events, err := provider.ChatStream(context.Background(), ChatRequest{Messages: []Message{
				{Role: RoleUser, Content: "use calculator to compute 13 * 7"},
				{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_fake_calculator", Type: "function", Function: ToolCallFunction{Name: "calculator", Arguments: `{"a":13,"b":7,"op":"mul"}`}}}},
				{Role: RoleTool, ToolCallID: "call_fake_calculator", Content: "91"},
				{Role: RoleAssistant, Content: "13 * 7 = 91"},
				{Role: RoleUser, Content: prompt},
			}})
			if err != nil {
				t.Fatalf("ChatStream() error = %v", err)
			}

			message := collectFakeDoneMessage(t, events)
			if message.Content != "previous result was 91" {
				t.Fatalf("previous result answer = %q, want previous result was 91", message.Content)
			}
		})
	}
}

func TestFakeProviderReportsMissingPreviousResultWithoutToolHistory(t *testing.T) {
	provider := &FakeProvider{model: "fake-tool-model"}
	events, err := provider.ChatStream(context.Background(), ChatRequest{Messages: []Message{
		{Role: RoleUser, Content: "what was the previous result?"},
	}})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	message := collectFakeDoneMessage(t, events)
	if message.Content != "no previous result found" {
		t.Fatalf("previous result answer = %q, want no previous result found", message.Content)
	}
}

func collectFakeDoneMessage(t *testing.T, events <-chan ChatStreamEvent) Message {
	t.Helper()
	for event := range events {
		if event.Type == ChatStreamEventTypeDone {
			return event.Message
		}
	}
	t.Fatal("stream ended without done event")
	return Message{}
}
