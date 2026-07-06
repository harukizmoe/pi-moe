package agent

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

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

type runMessagesRunner interface {
	RunMessages(context.Context, []llms.Message) (*RunResult, error)
}

func runMessages(t *testing.T, a *Agent, ctx context.Context, messages []llms.Message) (*RunResult, error) {
	t.Helper()

	runner, ok := any(a).(runMessagesRunner)
	if !ok {
		t.Fatal("*Agent does not implement RunMessages(context.Context, []llms.Message) (*RunResult, error)")
	}

	return runner.RunMessages(ctx, messages)
}

type runAgentMessagesRunner interface {
	RunAgentMessages(context.Context, []Message) (*RunResult, error)
}

func runAgentMessages(t *testing.T, a *Agent, ctx context.Context, messages []Message) (*RunResult, error) {
	t.Helper()

	runner, ok := any(a).(runAgentMessagesRunner)
	if !ok {
		t.Fatal("*Agent does not implement RunAgentMessages(context.Context, []Message) (*RunResult, error)")
	}
	return runner.RunAgentMessages(ctx, messages)
}

func assertMessagesEqual(t *testing.T, got, want []llms.Message) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("messages len = %d, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i].Role != want[i].Role {
			t.Fatalf("messages[%d].Role = %q, want %q", i, got[i].Role, want[i].Role)
		}
		if got[i].Content != want[i].Content {
			t.Fatalf("messages[%d].Content = %q, want %q", i, got[i].Content, want[i].Content)
		}
		if got[i].ToolCallID != want[i].ToolCallID {
			t.Fatalf("messages[%d].ToolCallID = %q, want %q", i, got[i].ToolCallID, want[i].ToolCallID)
		}
		if len(got[i].ToolCalls) != len(want[i].ToolCalls) {
			t.Fatalf("messages[%d].ToolCalls len = %d, want %d", i, len(got[i].ToolCalls), len(want[i].ToolCalls))
		}

		for j := range want[i].ToolCalls {
			gotCall := got[i].ToolCalls[j]
			wantCall := want[i].ToolCalls[j]
			if gotCall.ID != wantCall.ID {
				t.Fatalf("messages[%d].ToolCalls[%d].ID = %q, want %q", i, j, gotCall.ID, wantCall.ID)
			}
			if gotCall.Type != wantCall.Type {
				t.Fatalf("messages[%d].ToolCalls[%d].Type = %q, want %q", i, j, gotCall.Type, wantCall.Type)
			}
			if gotCall.Function.Name != wantCall.Function.Name {
				t.Fatalf("messages[%d].ToolCalls[%d].Function.Name = %q, want %q", i, j, gotCall.Function.Name, wantCall.Function.Name)
			}
			if gotCall.Function.Arguments != wantCall.Function.Arguments {
				t.Fatalf("messages[%d].ToolCalls[%d].Function.Arguments = %q, want %q", i, j, gotCall.Function.Arguments, wantCall.Function.Arguments)
			}
		}
	}
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

type agentStreamer interface {
	Stream(context.Context, string) <-chan Event
}

var _ agentStreamer = (*Agent)(nil)

func collectStreamEvents(t *testing.T, stream <-chan Event) []Event {
	t.Helper()

	var events []Event
	for event := range stream {
		events = append(events, event)
	}

	return events
}

func TestAgentStreamReturnsTypedToolCallingEvents(t *testing.T) {
	provider, err := llms.NewFakeProvider(llms.ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	recorder := &recordingProvider{inner: provider}
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	a := New(recorder, registry, "fake-tool-model")
	events := collectStreamEvents(t, a.Stream(context.Background(), "use calculator to compute 13 * 7"))

	if len(events) != 7 {
		t.Fatalf("stream events len = %d, want 7", len(events))
	}
	if len(recorder.requests) != 2 {
		t.Fatalf("provider requests len = %d, want 2", len(recorder.requests))
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

func TestAgentRunResultReturnsAnswerWithoutToolCalls(t *testing.T) {
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		return &llms.ChatResponse{Message: llms.Message{
			Role:    llms.RoleAssistant,
			Content: "done without tools",
		}}, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := a.RunResult(context.Background(), "just answer directly")
	if err != nil {
		t.Fatalf("RunResult() error = %v", err)
	}
	if got == nil {
		t.Fatal("RunResult() result = nil")
	}
	if got.Answer != "done without tools" {
		t.Fatalf("RunResult().Answer = %q", got.Answer)
	}
	if got.ToolRounds != 0 {
		t.Fatalf("RunResult().ToolRounds = %d, want 0", got.ToolRounds)
	}
	if len(got.Steps) != 0 {
		t.Fatalf("RunResult().Steps len = %d, want 0", len(got.Steps))
	}
	if len(provider.requests) != 1 {
		t.Fatalf("chat requests len = %d, want 1", len(provider.requests))
	}
}

func TestAgentRunResultWrapsProviderErrorWithChatRoundContext(t *testing.T) {
	sentinel := errors.New("provider chat failed")
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		return nil, sentinel
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := a.RunResult(context.Background(), "just answer directly")
	if err == nil {
		t.Fatalf("RunResult() error = nil, result = %#v", got)
	}
	if !strings.Contains(err.Error(), "llm chat round 1") {
		t.Fatalf("RunResult() error = %q, want chat round context", err.Error())
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunResult() error = %v, want to wrap sentinel %v", err, sentinel)
	}
}

func TestAgentRunResultReturnsContextCancellationWhenStreamClosesWithoutTerminalError(t *testing.T) {
	oldMaxProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(oldMaxProcs)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		cancel()
		return &llms.ChatResponse{Message: llms.Message{
			Role:    llms.RoleAssistant,
			Content: "answer dropped by cancellation",
		}}, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := a.RunResult(ctx, "just answer directly")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunResult() error = %v, want context cancellation", err)
	}
	if got == nil {
		t.Fatal("RunResult() result = nil, want partial result")
	}
	if got.Answer != "" {
		t.Fatalf("RunResult().Answer = %q, want empty when cancellation drops terminal events", got.Answer)
	}
}

func TestAgentStreamMessagesClosesWithoutEventWhenContextAlreadyCanceledAndHistoryInvalid(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		t.Fatal("provider.Chat() should not be called for invalid history")
		return nil, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	stream := a.StreamMessages(ctx, []llms.Message{
		{Role: llms.RoleAssistant},
		{Role: llms.RoleUser, Content: "continue"},
	})

	select {
	case event, ok := <-stream:
		if ok {
			t.Fatalf("StreamMessages() delivered %T (%#v), want closed stream without event", event, event)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("StreamMessages() did not close promptly")
	}
}

func TestAgentRunAgentMessagesRejectsEmptyAssistantResponse(t *testing.T) {
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		return &llms.ChatResponse{Message: llms.Message{Role: llms.RoleAssistant}}, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := runAgentMessages(t, a, context.Background(), []Message{
		UserMessage{Content: "answer directly"},
	})
	if err == nil {
		t.Fatalf("RunAgentMessages() error = nil, result = %#v", got)
	}
	if !strings.Contains(err.Error(), "assistant message must have content or tool calls") {
		t.Fatalf("RunAgentMessages() error = %q, want assistant validation", err.Error())
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests len = %d, want 1", len(provider.requests))
	}
}

func TestAgentRunResultRecordsToolTraceAcrossRounds(t *testing.T) {
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
	got, err := a.RunResult(context.Background(), "compute (2 + 3) * 4")
	if err != nil {
		t.Fatalf("RunResult() error = %v", err)
	}
	if got == nil {
		t.Fatal("RunResult() result = nil")
	}
	if got.Answer != "final answer: 20" {
		t.Fatalf("RunResult().Answer = %q", got.Answer)
	}
	if got.ToolRounds != 2 {
		t.Fatalf("RunResult().ToolRounds = %d, want 2", got.ToolRounds)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("RunResult().Steps len = %d, want 2", len(got.Steps))
	}

	step1 := got.Steps[0]
	if step1.ToolCallID != "call_step_1" {
		t.Fatalf("RunResult().Steps[0].ToolCallID = %q", step1.ToolCallID)
	}
	if step1.ToolName != "calculator" {
		t.Fatalf("RunResult().Steps[0].ToolName = %q", step1.ToolName)
	}
	if step1.Arguments != `{"a":2,"b":3,"op":"add"}` {
		t.Fatalf("RunResult().Steps[0].Arguments = %q", step1.Arguments)
	}
	if step1.Result != "5" {
		t.Fatalf("RunResult().Steps[0].Result = %q", step1.Result)
	}
	if step1.Error != "" {
		t.Fatalf("RunResult().Steps[0].Error = %q, want empty", step1.Error)
	}

	step2 := got.Steps[1]
	if step2.ToolCallID != "call_step_2" {
		t.Fatalf("RunResult().Steps[1].ToolCallID = %q", step2.ToolCallID)
	}
	if step2.ToolName != "calculator" {
		t.Fatalf("RunResult().Steps[1].ToolName = %q", step2.ToolName)
	}
	if step2.Arguments != `{"a":5,"b":4,"op":"mul"}` {
		t.Fatalf("RunResult().Steps[1].Arguments = %q", step2.Arguments)
	}
	if step2.Result != "20" {
		t.Fatalf("RunResult().Steps[1].Result = %q", step2.Result)
	}
	if step2.Error != "" {
		t.Fatalf("RunResult().Steps[1].Error = %q, want empty", step2.Error)
	}
}

func TestAgentRunAgentMessagesContinuesAfterToolErrorAndReturnsFinalAnswer(t *testing.T) {
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
					calculatorToolCall("call_bad_args", `{"a":1`),
				},
			}}, nil
		case 2:
			got := req.Messages[len(req.Messages)-1]
			if got.Role != llms.RoleTool {
				t.Fatalf("second request last role = %q, want tool", got.Role)
			}
			if got.ToolCallID != "call_bad_args" {
				t.Fatalf("second request ToolCallID = %q, want call_bad_args", got.ToolCallID)
			}
			if !strings.HasPrefix(got.Content, `tool "calculator" failed: `) {
				t.Fatalf("second request tool content = %q, want sanitized failure summary", got.Content)
			}
			if !strings.Contains(got.Content, "decode calculator arguments") {
				t.Fatalf("second request tool content = %q, want calculator decode context", got.Content)
			}
			return &llms.ChatResponse{Message: llms.Message{
				Role:    llms.RoleAssistant,
				Content: "I couldn't use calculator because the arguments were malformed.",
			}}, nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := New(provider, registry, "fake-tool-model")
	got, err := runAgentMessages(t, a, context.Background(), []Message{
		UserMessage{Content: "try calculator with bad arguments"},
	})
	if err != nil {
		t.Fatalf("RunAgentMessages() error = %v", err)
	}
	if got == nil {
		t.Fatal("RunAgentMessages() result = nil")
	}
	if got.Answer != "I couldn't use calculator because the arguments were malformed." {
		t.Fatalf("RunAgentMessages().Answer = %q", got.Answer)
	}
	if got.ToolRounds != 1 {
		t.Fatalf("RunAgentMessages().ToolRounds = %d, want 1", got.ToolRounds)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("RunAgentMessages().Steps len = %d, want 1", len(got.Steps))
	}
	step := got.Steps[0]
	if step.Result != "" {
		t.Fatalf("RunAgentMessages().Steps[0].Result = %q, want empty", step.Result)
	}
	if step.Error == "" || !strings.Contains(step.Error, "decode calculator arguments") {
		t.Fatalf("RunAgentMessages().Steps[0].Error = %q, want calculator decode failure", step.Error)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider requests len = %d, want 2", len(provider.requests))
	}
}

func TestAgentRunAgentMessagesReturnsMaxStepsErrorWhenModelKeepsRetryingAfterToolError(t *testing.T) {
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
					calculatorToolCall("call_bad_args", `{"a":1`),
				},
			}}, nil
		case 2:
			got := req.Messages[len(req.Messages)-1]
			if got.Role != llms.RoleTool || !strings.HasPrefix(got.Content, `tool "calculator" failed: `) {
				t.Fatalf("second request last message = %#v, want sanitized tool failure", got)
			}
			return &llms.ChatResponse{Message: llms.Message{
				Role: llms.RoleAssistant,
				ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_retry", `{"a":1`),
				},
			}}, nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := NewWithOptions(provider, registry, "fake-tool-model", Options{MaxSteps: 1})
	got, err := runAgentMessages(t, a, context.Background(), []Message{
		UserMessage{Content: "keep trying even after tool failure"},
	})
	if err == nil {
		t.Fatal("RunAgentMessages() error = nil, want max steps error")
	}
	if !strings.Contains(err.Error(), "max steps") {
		t.Fatalf("RunAgentMessages() error = %v, want max steps message", err)
	}
	if got == nil {
		t.Fatal("RunAgentMessages() result = nil, want retained step trace")
	}
	if got.ToolRounds != 1 {
		t.Fatalf("RunAgentMessages().ToolRounds = %d, want 1", got.ToolRounds)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("RunAgentMessages().Steps len = %d, want 1", len(got.Steps))
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider requests len = %d, want 2", len(provider.requests))
	}
}

func TestAgentRunAgentMessagesForwardsSemanticHistoryToProvider(t *testing.T) {
	history := []Message{
		UserMessage{Content: "what is 2 + 2?"},
		AssistantMessage{ToolCalls: []llms.ToolCall{
			calculatorToolCall("call_1", `{"a":2,"b":2,"op":"add"}`),
		}},
		ToolResultMessage{ToolCallID: "call_1", ToolName: "calculator", Content: "4"},
		AssistantMessage{Content: "2 + 2 = 4."},
		UserMessage{Content: "multiply that by 3"},
	}

	want := []llms.Message{
		{Role: llms.RoleUser, Content: "what is 2 + 2?"},
		{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{
			calculatorToolCall("call_1", `{"a":2,"b":2,"op":"add"}`),
		}},
		{Role: llms.RoleTool, ToolCallID: "call_1", Content: "4"},
		{Role: llms.RoleAssistant, Content: "2 + 2 = 4."},
		{Role: llms.RoleUser, Content: "multiply that by 3"},
	}

	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		assertMessagesEqual(t, req.Messages, want)
		return &llms.ChatResponse{Message: llms.Message{
			Role:    llms.RoleAssistant,
			Content: "4 * 3 = 12",
		}}, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := runAgentMessages(t, a, context.Background(), history)
	if err != nil {
		t.Fatalf("RunAgentMessages() error = %v", err)
	}
	if got == nil || got.Answer != "4 * 3 = 12" {
		t.Fatalf("RunAgentMessages().Answer = %#v, want %q", got, "4 * 3 = 12")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests len = %d, want 1", len(provider.requests))
	}
}

func TestAgentRunAgentMessagesRejectsInvalidHistoryAndSemanticMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
	}{
		{name: "nil history", messages: nil},
		{name: "empty history", messages: []Message{}},
		{name: "last message not user", messages: []Message{
			AssistantMessage{Content: "hi"},
		}},
		{name: "last user message whitespace", messages: []Message{
			UserMessage{Content: " \n\t "},
		}},
		{name: "earlier user empty", messages: []Message{
			UserMessage{Content: "   "},
			UserMessage{Content: "continue"},
		}},
		{name: "assistant empty", messages: []Message{
			AssistantMessage{},
			UserMessage{Content: "continue"},
		}},
		{name: "tool result missing tool call id", messages: []Message{
			ToolResultMessage{ToolName: "calculator", Content: "3"},
			UserMessage{Content: "continue"},
		}},
		{name: "tool result missing tool name", messages: []Message{
			ToolResultMessage{ToolCallID: "call_1", Content: "3"},
			UserMessage{Content: "continue"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
				t.Fatal("provider.Chat() should not be called for invalid semantic history")
				return nil, nil
			})}

			a := New(provider, tools.NewRegistry(), "fake-tool-model")
			got, err := runAgentMessages(t, a, context.Background(), tt.messages)
			if err == nil {
				t.Fatalf("RunAgentMessages() error = nil, result = %#v", got)
			}
			if len(provider.requests) != 0 {
				t.Fatalf("provider requests len = %d, want 0", len(provider.requests))
			}
		})
	}
}

func TestAgentRunMessagesForwardsWideHistoryWithPriorToolResult(t *testing.T) {
	history := []llms.Message{
		{Role: llms.RoleUser, Content: "what is 2 + 2?"},
		{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{
			calculatorToolCall("call_1", `{"a":2,"b":2,"op":"add"}`),
		}},
		{Role: llms.RoleTool, ToolCallID: "call_1", Content: "4"},
		{Role: llms.RoleAssistant, Content: "2 + 2 = 4."},
		{Role: llms.RoleUser, Content: "multiply that by 3"},
	}

	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		assertMessagesEqual(t, req.Messages, history)
		return &llms.ChatResponse{Message: llms.Message{
			Role:    llms.RoleAssistant,
			Content: "4 * 3 = 12",
		}}, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := runMessages(t, a, context.Background(), history)
	if err != nil {
		t.Fatalf("RunMessages() error = %v", err)
	}
	if got == nil || got.Answer != "4 * 3 = 12" {
		t.Fatalf("RunMessages().Answer = %#v, want %q", got, "4 * 3 = 12")
	}
}

func TestAgentRunMessagesRejectsIllegalWideMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []llms.Message
	}{
		{
			name: "user with tool calls",
			messages: []llms.Message{
				{Role: llms.RoleUser, Content: "hello", ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_1", `{"a":1,"b":2,"op":"add"}`),
				}},
				{Role: llms.RoleUser, Content: "continue"},
			},
		},
		{
			name: "assistant with tool call id",
			messages: []llms.Message{
				{Role: llms.RoleAssistant, Content: "hi", ToolCallID: "call_1"},
				{Role: llms.RoleUser, Content: "continue"},
			},
		},
		{
			name: "assistant empty",
			messages: []llms.Message{
				{Role: llms.RoleAssistant},
				{Role: llms.RoleUser, Content: "continue"},
			},
		},
		{
			name: "tool result missing tool call id",
			messages: []llms.Message{
				{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{
					calculatorToolCall("call_1", `{"a":1,"b":2,"op":"add"}`),
				}},
				{Role: llms.RoleTool, Content: "3"},
				{Role: llms.RoleUser, Content: "continue"},
			},
		},
		{
			name: "tool result without matching assistant tool call",
			messages: []llms.Message{
				{Role: llms.RoleTool, ToolCallID: "missing", Content: "3"},
				{Role: llms.RoleUser, Content: "continue"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
				t.Fatal("provider.Chat() should not be called for illegal llms.Message history")
				return nil, nil
			})}
			a := New(provider, tools.NewRegistry(), "fake-tool-model")

			got, err := runMessages(t, a, context.Background(), tt.messages)
			if err == nil {
				t.Fatalf("RunMessages() error = nil, result = %#v", got)
			}
			if len(provider.requests) != 0 {
				t.Fatalf("provider requests len = %d, want 0", len(provider.requests))
			}
		})
	}
}

func TestAgentRunMessagesForwardsProvidedHistoryToProvider(t *testing.T) {
	history := []llms.Message{
		{Role: llms.RoleUser, Content: "what is 2 + 2?"},
		{Role: llms.RoleAssistant, Content: "2 + 2 = 4."},
		{Role: llms.RoleUser, Content: "multiply that by 3"},
	}

	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		return &llms.ChatResponse{Message: llms.Message{
			Role:    llms.RoleAssistant,
			Content: "4 * 3 = 12",
		}}, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := runMessages(t, a, context.Background(), history)
	if err != nil {
		t.Fatalf("RunMessages() error = %v", err)
	}
	if got == nil {
		t.Fatal("RunMessages() result = nil")
	}
	if got.Answer != "4 * 3 = 12" {
		t.Fatalf("RunMessages().Answer = %q", got.Answer)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("chat requests len = %d, want 1", len(provider.requests))
	}

	assertMessagesEqual(t, provider.requests[0].Messages, history)
}

func TestAgentRunMessagesContinuesToolCallingFromProvidedHistory(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	history := []llms.Message{
		{Role: llms.RoleUser, Content: "what is 4 * 4?"},
		{Role: llms.RoleAssistant, Content: "4 * 4 = 16."},
		{Role: llms.RoleUser, Content: "now multiply that by 5"},
	}
	assistantToolCall := llms.Message{
		Role: llms.RoleAssistant,
		ToolCalls: []llms.ToolCall{
			calculatorToolCall("call_history_mul", `{"a":16,"b":5,"op":"mul"}`),
		},
	}
	toolResult := llms.Message{Role: llms.RoleTool, Content: "80", ToolCallID: "call_history_mul"}

	round := 0
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		round++
		switch round {
		case 1:
			assertMessagesEqual(t, req.Messages, history)
			return &llms.ChatResponse{Message: assistantToolCall}, nil
		case 2:
			want := append(append([]llms.Message{}, history...), assistantToolCall, toolResult)
			assertMessagesEqual(t, req.Messages, want)
			return &llms.ChatResponse{Message: llms.Message{
				Role:    llms.RoleAssistant,
				Content: "16 * 5 = 80",
			}}, nil
		default:
			t.Fatalf("unexpected chat round = %d", round)
			return nil, nil
		}
	})}

	a := New(provider, registry, "fake-tool-model")
	got, err := runMessages(t, a, context.Background(), history)
	if err != nil {
		t.Fatalf("RunMessages() error = %v", err)
	}
	if got == nil {
		t.Fatal("RunMessages() result = nil")
	}
	if got.Answer != "16 * 5 = 80" {
		t.Fatalf("RunMessages().Answer = %q", got.Answer)
	}
	if got.ToolRounds != 1 {
		t.Fatalf("RunMessages().ToolRounds = %d, want 1", got.ToolRounds)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("RunMessages().Steps len = %d, want 1", len(got.Steps))
	}
	if len(provider.requests) != 2 {
		t.Fatalf("chat requests len = %d, want 2", len(provider.requests))
	}

	step := got.Steps[0]
	if step.ToolCallID != "call_history_mul" {
		t.Fatalf("RunMessages().Steps[0].ToolCallID = %q", step.ToolCallID)
	}
	if step.ToolName != "calculator" {
		t.Fatalf("RunMessages().Steps[0].ToolName = %q", step.ToolName)
	}
	if step.Arguments != `{"a":16,"b":5,"op":"mul"}` {
		t.Fatalf("RunMessages().Steps[0].Arguments = %q", step.Arguments)
	}
	if step.Result != "80" {
		t.Fatalf("RunMessages().Steps[0].Result = %q", step.Result)
	}
	if step.Error != "" {
		t.Fatalf("RunMessages().Steps[0].Error = %q, want empty", step.Error)
	}
}

func TestAgentRunMessagesRejectsInvalidHistory(t *testing.T) {
	tests := []struct {
		name     string
		messages []llms.Message
		wantErr  string
	}{
		{
			name:     "nil messages",
			messages: nil,
			wantErr:  "messages must not be empty",
		},
		{
			name:     "empty messages",
			messages: []llms.Message{},
			wantErr:  "messages must not be empty",
		},
		{
			name: "last message not user",
			messages: []llms.Message{
				{Role: llms.RoleUser, Content: "hello"},
				{Role: llms.RoleAssistant, Content: "hi there"},
			},
			wantErr: "last message must be a non-empty user message",
		},
		{
			name: "last user message empty",
			messages: []llms.Message{
				{Role: llms.RoleUser, Content: "hello"},
				{Role: llms.RoleUser, Content: ""},
			},
			wantErr: "last message must be a non-empty user message",
		},
		{
			name: "last user message whitespace",
			messages: []llms.Message{
				{Role: llms.RoleUser, Content: "hello"},
				{Role: llms.RoleUser, Content: "   \n\t  "},
			},
			wantErr: "last message must be a non-empty user message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
				t.Fatal("provider.Chat() should not be called for invalid history")
				return nil, nil
			})}

			a := New(provider, tools.NewRegistry(), "fake-tool-model")
			got, err := runMessages(t, a, context.Background(), tt.messages)
			if err == nil {
				t.Fatalf("RunMessages() error = nil, result = %#v", got)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("RunMessages() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
			if len(provider.requests) != 0 {
				t.Fatalf("provider requests len = %d, want 0", len(provider.requests))
			}
		})
	}
}
