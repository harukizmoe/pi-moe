package agent

import (
	"context"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

type recordingProvider struct {
	inner    llms.Provider
	requests []llms.ChatRequest
}

func (p *recordingProvider) Chat(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
	messages := make([]llms.Message, len(req.Messages))
	for i, msg := range req.Messages {
		toolCalls := append([]llms.ToolCall(nil), msg.ToolCalls...)
		messages[i] = llms.Message{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCalls:  toolCalls,
			ToolCallID: msg.ToolCallID,
		}
	}

	copied := llms.ChatRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    append([]llms.Tool(nil), req.Tools...),
	}
	p.requests = append(p.requests, copied)

	return p.inner.Chat(ctx, req)
}

type chatFunc func(context.Context, llms.ChatRequest) (*llms.ChatResponse, error)

func (f chatFunc) Chat(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
	return f(ctx, req)
}

func TestAgentRunExecutesToolCall(t *testing.T) {
	provider, err := llms.NewFakeProvider(llms.ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	recorder := &recordingProvider{inner: provider}
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	a := New(recorder, registry, "fake-tool-model")
	got, err := a.Run(context.Background(), "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != "13 * 7 = 91" {
		t.Fatalf("answer = %q", got)
	}

	if len(recorder.requests) != 2 {
		t.Fatalf("chat requests len = %d", len(recorder.requests))
	}

	first := recorder.requests[0]
	if len(first.Messages) != 1 {
		t.Fatalf("first messages len = %d", len(first.Messages))
	}
	if first.Messages[0].Role != llms.RoleUser {
		t.Fatalf("first user role = %q", first.Messages[0].Role)
	}
	if first.Messages[0].Content != "use calculator to compute 13 * 7" {
		t.Fatalf("first user content = %q", first.Messages[0].Content)
	}
	if len(first.Tools) != 1 {
		t.Fatalf("first tools len = %d", len(first.Tools))
	}
	if first.Tools[0].Function.Name != "calculator" {
		t.Fatalf("first tool name = %q", first.Tools[0].Function.Name)
	}

	second := recorder.requests[1]
	if len(second.Messages) != 3 {
		t.Fatalf("second messages len = %d", len(second.Messages))
	}
	assistant := second.Messages[1]
	if assistant.Role != llms.RoleAssistant {
		t.Fatalf("assistant role = %q", assistant.Role)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls len = %d", len(assistant.ToolCalls))
	}
	call := assistant.ToolCalls[0]
	if call.Function.Name != "calculator" {
		t.Fatalf("assistant tool name = %q", call.Function.Name)
	}
	if call.Function.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("assistant tool args = %q", call.Function.Arguments)
	}

	toolMessage := second.Messages[2]
	if toolMessage.Role != llms.RoleTool {
		t.Fatalf("tool role = %q", toolMessage.Role)
	}
	if toolMessage.ToolCallID != call.ID {
		t.Fatalf("tool call id = %q, want %q", toolMessage.ToolCallID, call.ID)
	}
	if toolMessage.Content != "91" {
		t.Fatalf("tool content = %q", toolMessage.Content)
	}
}

func TestAgentRunRejectsSecondRoundToolCall(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		round++
		switch round {
		case 1:
			return &llms.ChatResponse{Message: llms.Message{
				Role: llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{{
					ID:   "call_first_round",
					Type: "function",
					Function: llms.ToolCallFunction{
						Name:      "calculator",
						Arguments: `{"a":13,"b":7,"op":"mul"}`,
					},
				}},
			}}, nil
		case 2:
			return &llms.ChatResponse{Message: llms.Message{
				Role:    llms.RoleAssistant,
				Content: "second-round tool call should be rejected",
				ToolCalls: []llms.ToolCall{{
					ID:   "call_second_round",
					Type: "function",
					Function: llms.ToolCallFunction{
						Name:      "calculator",
						Arguments: `{"a":1,"b":2,"op":"add"}`,
					},
				}},
			}}, nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := New(provider, registry, "fake-tool-model")
	got, err := a.Run(context.Background(), "use calculator twice")
	if err == nil {
		t.Fatal("Run() error = nil, want explicit second-round tool call error")
	}
	if got != "" {
		t.Fatalf("Run() answer = %q, want empty string on error", got)
	}
	if !strings.Contains(err.Error(), "second tool-calling round") {
		t.Fatalf("Run() error = %v, want second-round tool call message", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("chat requests len = %d", len(provider.requests))
	}
}
