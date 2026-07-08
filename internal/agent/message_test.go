package agent

import (
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/llms"
)

func TestToLLMMessagesAcceptsAssistantToolCallBlock(t *testing.T) {
	messages := []Message{
		UserMessage{Content: "what is 2 + 2?"},
		AssistantMessage{ToolCalls: []llms.ToolCall{
			calculatorToolCall("call_1", `{"a":2,"b":2,"op":"add"}`),
			calculatorToolCall("call_2", `{"a":4,"b":3,"op":"mul"}`),
		}},
		ToolResultMessage{ToolCallID: "call_1", ToolName: "calculator", Content: "4"},
		ToolResultMessage{ToolCallID: "call_2", ToolName: "calculator", Content: "12"},
		UserMessage{Content: "thanks"},
	}

	got, err := toLLMMessages(messages)
	if err != nil {
		t.Fatalf("toLLMMessages() error = %v", err)
	}

	want := []llms.Message{
		{Role: llms.RoleUser, Content: "what is 2 + 2?"},
		{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{
			calculatorToolCall("call_1", `{"a":2,"b":2,"op":"add"}`),
			calculatorToolCall("call_2", `{"a":4,"b":3,"op":"mul"}`),
		}},
		{Role: llms.RoleTool, ToolCallID: "call_1", Content: "4"},
		{Role: llms.RoleTool, ToolCallID: "call_2", Content: "12"},
		{Role: llms.RoleUser, Content: "thanks"},
	}

	assertMessagesEqual(t, got, want)
}

func TestToLLMMessagesRejectsInvalidToolResultOrdering(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		wantAll  []string
	}{
		{
			name: "lone tool result after user",
			messages: []Message{
				UserMessage{Content: "what is 2 + 2?"},
				ToolResultMessage{ToolCallID: "call_orphan", ToolName: "calculator", Content: "4"},
				UserMessage{Content: "continue"},
			},
			wantAll: []string{"tool result", "pending assistant tool call"},
		},
		{
			name: "user message before pending tool result",
			messages: []Message{
				UserMessage{Content: "what is 2 + 2?"},
				AssistantMessage{ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_1", `{"a":2,"b":2,"op":"add"}`),
				}},
				UserMessage{Content: "continue"},
			},
			wantAll: []string{"missing tool result"},
		},
		{
			name: "tool result id mismatch",
			messages: []Message{
				UserMessage{Content: "what is 2 + 2?"},
				AssistantMessage{ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_1", `{"a":2,"b":2,"op":"add"}`),
				}},
				ToolResultMessage{ToolCallID: "call_2", ToolName: "calculator", Content: "4"},
				UserMessage{Content: "continue"},
			},
			wantAll: []string{"tool result", "mismatch"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toLLMMessages(tt.messages)
			if err == nil {
				t.Fatalf("toLLMMessages() error = nil, want validation error; got messages = %#v", got)
			}
			for _, want := range tt.wantAll {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("toLLMMessages() error = %q, want substring %q", err.Error(), want)
				}
			}
		})
	}
}
