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

type recordingLogger struct {
	messages []string
}

func (l *recordingLogger) Debug(ctx context.Context, msg string, attrs ...any) {
	l.messages = append(l.messages, msg)
}

func (l *recordingLogger) Info(ctx context.Context, msg string, attrs ...any) {
	l.messages = append(l.messages, msg)
}

func (l *recordingLogger) Error(ctx context.Context, msg string, attrs ...any) {
	l.messages = append(l.messages, msg)
}

func (l *recordingLogger) contains(msg string) bool {
	for _, got := range l.messages {
		if got == msg {
			return true
		}
	}
	return false
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

func TestAgentRunLogsToolCallingFlow(t *testing.T) {
	provider, err := llms.NewFakeProvider(llms.ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})
	log := &recordingLogger{}

	a := NewWithLogger(provider, registry, "fake-tool-model", log)
	got, err := a.Run(context.Background(), "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != "13 * 7 = 91" {
		t.Fatalf("answer = %q", got)
	}

	for _, want := range []string{
		"agent.run.start",
		"agent.llm.first.request",
		"agent.tool_calls.received",
		"agent.tool.call",
		"agent.tool.result",
		"agent.llm.final.request",
		"agent.run.done",
	} {
		if !log.contains(want) {
			t.Fatalf("logged messages = %v, want %q", log.messages, want)
		}
	}
}

func TestAgentRunDefaultAllowsSecondRoundToolCall(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		round++
		switch round {
		case 1:
			return &llms.ChatResponse{Message: llms.Message{
				Role:      llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{calculatorToolCall("call_first_round", `{"a":13,"b":7,"op":"mul"}`)},
			}}, nil
		case 2:
			return &llms.ChatResponse{Message: llms.Message{
				Role:      llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{calculatorToolCall("call_second_round", `{"a":1,"b":2,"op":"add"}`)},
			}}, nil
		case 3:
			return &llms.ChatResponse{Message: llms.Message{
				Role:    llms.RoleAssistant,
				Content: "second round completed",
			}}, nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := New(provider, registry, "fake-tool-model")
	got, err := a.Run(context.Background(), "use calculator twice")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != "second round completed" {
		t.Fatalf("Run() answer = %q", got)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("chat requests len = %d", len(provider.requests))
	}
}

func calculatorToolCall(id, arguments string) llms.ToolCall {
	return llms.ToolCall{
		ID:   id,
		Type: "function",
		Function: llms.ToolCallFunction{
			Name:      "calculator",
			Arguments: arguments,
		},
	}
}

func TestAgentRunSupportsMultipleToolRoundsWithinMaxSteps(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		round++
		switch round {
		case 1:
			return &llms.ChatResponse{Message: llms.Message{
				Role: llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_step_1", `{"a":2,"b":3,"op":"add"}`),
				},
			}}, nil
		case 2:
			if got := req.Messages[len(req.Messages)-1]; got.Role != llms.RoleTool || got.Content != "5" {
				t.Fatalf("second request last message = %#v, want tool result 5", got)
			}
			return &llms.ChatResponse{Message: llms.Message{
				Role: llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_step_2", `{"a":5,"b":4,"op":"mul"}`),
				},
			}}, nil
		case 3:
			if got := req.Messages[len(req.Messages)-1]; got.Role != llms.RoleTool || got.Content != "20" {
				t.Fatalf("third request last message = %#v, want tool result 20", got)
			}
			return &llms.ChatResponse{Message: llms.Message{
				Role:    llms.RoleAssistant,
				Content: "final answer: 20",
			}}, nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := NewWithOptions(provider, registry, "fake-tool-model", Options{MaxSteps: 2})
	got, err := a.Run(context.Background(), "compute (2 + 3) * 4")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != "final answer: 20" {
		t.Fatalf("Run() answer = %q", got)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("chat requests len = %d", len(provider.requests))
	}
}

func TestAgentRunReturnsMaxStepsErrorWhenToolLoopExceedsLimit(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	round := 0
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		round++
		switch round {
		case 1:
			return &llms.ChatResponse{Message: llms.Message{
				Role: llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_step_1", `{"a":2,"b":3,"op":"add"}`),
				},
			}}, nil
		case 2:
			if got := req.Messages[len(req.Messages)-1]; got.Role != llms.RoleTool || got.Content != "5" {
				t.Fatalf("second request last message = %#v, want tool result 5", got)
			}
			return &llms.ChatResponse{Message: llms.Message{
				Role: llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_step_2", `{"a":5,"b":4,"op":"mul"}`),
				},
			}}, nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := NewWithOptions(provider, registry, "fake-tool-model", Options{MaxSteps: 1})
	got, err := a.Run(context.Background(), "compute (2 + 3) * 4")
	if err == nil {
		t.Fatal("Run() error = nil, want max-steps error")
	}
	if got != "" {
		t.Fatalf("Run() answer = %q, want empty string on error", got)
	}
	if !strings.Contains(err.Error(), "max steps") {
		t.Fatalf("Run() error = %v, want max steps message", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("chat requests len = %d", len(provider.requests))
	}
}

func TestAgentRunEmitsOrderedToolAndFinalEvents(t *testing.T) {
	provider, err := llms.NewFakeProvider(llms.ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	var events []Event
	a := NewWithOptions(provider, registry, "fake-tool-model", Options{
		MaxSteps: 1,
		OnEvent: func(event Event) {
			events = append(events, event)
		},
	})

	got, err := a.Run(context.Background(), "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != "13 * 7 = 91" {
		t.Fatalf("Run() answer = %q", got)
	}

	wantTypes := []EventType{EventToolCall, EventToolResult, EventFinal}
	if len(events) != len(wantTypes) {
		t.Fatalf("events len = %d, want %d", len(events), len(wantTypes))
	}
	for i, wantType := range wantTypes {
		if events[i].Type != wantType {
			t.Fatalf("event[%d].Type = %q, want %q", i, events[i].Type, wantType)
		}
	}
	if events[len(events)-1].Message != got {
		t.Fatalf("final event message = %q, want %q", events[len(events)-1].Message, got)
	}
}
