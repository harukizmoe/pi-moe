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

func runAgentText(t *testing.T, a *Agent, ctx context.Context, input string) (string, error) {
	t.Helper()

	result, err := runAgentMessages(t, a, ctx, []Message{UserMessage{Content: input}})
	if err != nil {
		return "", err
	}
	return result.Answer, nil
}

func runAgentResult(t *testing.T, a *Agent, ctx context.Context, input string) (*RunResult, error) {
	t.Helper()

	return runAgentMessages(t, a, ctx, []Message{UserMessage{Content: input}})
}

func streamAgentText(a *Agent, ctx context.Context, input string) <-chan Event {
	return a.Stream(ctx, []Message{UserMessage{Content: input}})
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
	got, err := runAgentText(t, a, context.Background(), "use calculator to compute 13 * 7")
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
	got, err := runAgentText(t, a, context.Background(), "use calculator to compute 13 * 7")
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
	got, err := runAgentText(t, a, context.Background(), "use calculator twice")
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
	got, err := runAgentText(t, a, context.Background(), "compute (2 + 3) * 4")
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
	got, err := runAgentText(t, a, context.Background(), "compute (2 + 3) * 4")
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

func collectStreamEvents(t *testing.T, stream <-chan Event) []Event {
	t.Helper()

	var events []Event
	for event := range stream {
		events = append(events, event)
	}

	return events
}

func TestAgentStreamReturnsLifecycleToolCallingEvents(t *testing.T) {
	provider, err := llms.NewFakeProvider(llms.ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}

	recorder := &recordingProvider{inner: provider}
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	a := New(recorder, registry, "fake-tool-model")
	events := collectStreamEvents(t, streamAgentText(a, context.Background(), "use calculator to compute 13 * 7"))

	assertEventTypes(t, events,
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
	if len(recorder.requests) != 2 {
		t.Fatalf("provider requests len = %d, want 2", len(recorder.requests))
	}

	toolCallDelta := events[3].(MessageDeltaEvent)
	if toolCallDelta.Kind != MessageDeltaToolCall {
		t.Fatalf("event[3].Kind = %q, want %q", toolCallDelta.Kind, MessageDeltaToolCall)
	}
	if toolCallDelta.Delta != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("event[3].Delta = %q", toolCallDelta.Delta)
	}

	toolCall := events[5].(ToolExecutionStartEvent)
	if toolCall.ToolCallID != "call_fake_calculator" {
		t.Fatalf("ToolExecutionStartEvent.ToolCallID = %q, want %q", toolCall.ToolCallID, "call_fake_calculator")
	}
	if toolCall.ToolName != "calculator" {
		t.Fatalf("ToolExecutionStartEvent.ToolName = %q, want calculator", toolCall.ToolName)
	}
	if toolCall.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("ToolExecutionStartEvent.Arguments = %q", toolCall.Arguments)
	}

	toolResult := events[6].(ToolExecutionEndEvent)
	if toolResult.Result.ToolCallID != "call_fake_calculator" {
		t.Fatalf("ToolExecutionEndEvent.Result.ToolCallID = %q, want %q", toolResult.Result.ToolCallID, "call_fake_calculator")
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

	final := events[9].(MessageEndEvent)
	if final.Message.Content != "13 * 7 = 91" {
		t.Fatalf("MessageEndEvent.Message.Content = %q, want %q", final.Message.Content, "13 * 7 = 91")
	}
	if len(final.Message.ToolCalls) != 0 {
		t.Fatalf("MessageEndEvent.Message.ToolCalls len = %d, want 0", len(final.Message.ToolCalls))
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
	got, err := runAgentResult(t, a, context.Background(), "just answer directly")
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

func TestAgentRunAgentMessagesReturnsTypedTranscriptForFinalAnswer(t *testing.T) {
	history := []Message{
		UserMessage{Content: "what is 2 + 2?"},
		AssistantMessage{Content: "2 + 2 = 4."},
		UserMessage{Content: "multiply that by 3"},
	}

	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
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
	if got == nil {
		t.Fatal("RunAgentMessages() result = nil")
	}
	if len(got.Messages) != 4 {
		t.Fatalf("RunAgentMessages().Messages len = %d, want 4", len(got.Messages))
	}

	first, ok := got.Messages[0].(UserMessage)
	if !ok {
		t.Fatalf("RunAgentMessages().Messages[0] type = %T, want UserMessage", got.Messages[0])
	}
	if first.Content != "what is 2 + 2?" {
		t.Fatalf("RunAgentMessages().Messages[0].Content = %q", first.Content)
	}

	second, ok := got.Messages[1].(AssistantMessage)
	if !ok {
		t.Fatalf("RunAgentMessages().Messages[1] type = %T, want AssistantMessage", got.Messages[1])
	}
	if second.Content != "2 + 2 = 4." {
		t.Fatalf("RunAgentMessages().Messages[1].Content = %q", second.Content)
	}
	if len(second.ToolCalls) != 0 {
		t.Fatalf("RunAgentMessages().Messages[1].ToolCalls len = %d, want 0", len(second.ToolCalls))
	}

	third, ok := got.Messages[2].(UserMessage)
	if !ok {
		t.Fatalf("RunAgentMessages().Messages[2] type = %T, want UserMessage", got.Messages[2])
	}
	if third.Content != "multiply that by 3" {
		t.Fatalf("RunAgentMessages().Messages[2].Content = %q", third.Content)
	}

	final, ok := got.Messages[3].(AssistantMessage)
	if !ok {
		t.Fatalf("RunAgentMessages().Messages[3] type = %T, want AssistantMessage", got.Messages[3])
	}
	if final.Content != "4 * 3 = 12" {
		t.Fatalf("RunAgentMessages().Messages[3].Content = %q", final.Content)
	}
	if len(final.ToolCalls) != 0 {
		t.Fatalf("RunAgentMessages().Messages[3].ToolCalls len = %d, want 0", len(final.ToolCalls))
	}
}

func TestAgentRunAgentMessagesReturnsTypedTranscriptAcrossToolRound(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	history := []Message{
		UserMessage{Content: "what is 4 * 4?"},
		AssistantMessage{Content: "4 * 4 = 16."},
		UserMessage{Content: "now multiply that by 5"},
	}
	wantHistory := []llms.Message{
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
			assertMessagesEqual(t, req.Messages, wantHistory)
			return &llms.ChatResponse{Message: assistantToolCall}, nil
		case 2:
			want := append(append([]llms.Message{}, wantHistory...), assistantToolCall, toolResult)
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
	got, err := runAgentMessages(t, a, context.Background(), history)
	if err != nil {
		t.Fatalf("RunAgentMessages() error = %v", err)
	}
	if got == nil {
		t.Fatal("RunAgentMessages() result = nil")
	}
	if len(got.Messages) != 6 {
		t.Fatalf("RunAgentMessages().Messages len = %d, want 6", len(got.Messages))
	}

	first, ok := got.Messages[0].(UserMessage)
	if !ok {
		t.Fatalf("RunAgentMessages().Messages[0] type = %T, want UserMessage", got.Messages[0])
	}
	if first.Content != "what is 4 * 4?" {
		t.Fatalf("RunAgentMessages().Messages[0].Content = %q", first.Content)
	}

	second, ok := got.Messages[1].(AssistantMessage)
	if !ok {
		t.Fatalf("RunAgentMessages().Messages[1] type = %T, want AssistantMessage", got.Messages[1])
	}
	if second.Content != "4 * 4 = 16." {
		t.Fatalf("RunAgentMessages().Messages[1].Content = %q", second.Content)
	}
	if len(second.ToolCalls) != 0 {
		t.Fatalf("RunAgentMessages().Messages[1].ToolCalls len = %d, want 0", len(second.ToolCalls))
	}

	third, ok := got.Messages[2].(UserMessage)
	if !ok {
		t.Fatalf("RunAgentMessages().Messages[2] type = %T, want UserMessage", got.Messages[2])
	}
	if third.Content != "now multiply that by 5" {
		t.Fatalf("RunAgentMessages().Messages[2].Content = %q", third.Content)
	}

	fourth, ok := got.Messages[3].(AssistantMessage)
	if !ok {
		t.Fatalf("RunAgentMessages().Messages[3] type = %T, want AssistantMessage", got.Messages[3])
	}
	if fourth.Content != "" {
		t.Fatalf("RunAgentMessages().Messages[3].Content = %q, want empty tool-call assistant content", fourth.Content)
	}
	if len(fourth.ToolCalls) != 1 {
		t.Fatalf("RunAgentMessages().Messages[3].ToolCalls len = %d, want 1", len(fourth.ToolCalls))
	}
	if gotCall := fourth.ToolCalls[0]; gotCall.ID != "call_history_mul" || gotCall.Function.Name != "calculator" || gotCall.Function.Arguments != `{"a":16,"b":5,"op":"mul"}` {
		t.Fatalf("RunAgentMessages().Messages[3].ToolCalls[0] = %#v", gotCall)
	}

	fifth, ok := got.Messages[4].(ToolResultMessage)
	if !ok {
		t.Fatalf("RunAgentMessages().Messages[4] type = %T, want ToolResultMessage", got.Messages[4])
	}
	if fifth.ToolCallID != "call_history_mul" {
		t.Fatalf("RunAgentMessages().Messages[4].ToolCallID = %q", fifth.ToolCallID)
	}
	if fifth.ToolName != "calculator" {
		t.Fatalf("RunAgentMessages().Messages[4].ToolName = %q", fifth.ToolName)
	}
	if fifth.Content != "80" {
		t.Fatalf("RunAgentMessages().Messages[4].Content = %q", fifth.Content)
	}
	if fifth.IsError {
		t.Fatal("RunAgentMessages().Messages[4].IsError = true, want false")
	}

	final, ok := got.Messages[5].(AssistantMessage)
	if !ok {
		t.Fatalf("RunAgentMessages().Messages[5] type = %T, want AssistantMessage", got.Messages[5])
	}
	if final.Content != "16 * 5 = 80" {
		t.Fatalf("RunAgentMessages().Messages[5].Content = %q", final.Content)
	}
	if len(final.ToolCalls) != 0 {
		t.Fatalf("RunAgentMessages().Messages[5].ToolCalls len = %d, want 0", len(final.ToolCalls))
	}
}

func TestAgentRunResultWrapsProviderErrorWithChatRoundContext(t *testing.T) {
	sentinel := errors.New("provider chat failed")
	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		return nil, sentinel
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	got, err := runAgentResult(t, a, context.Background(), "just answer directly")
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
	got, err := runAgentResult(t, a, ctx, "just answer directly")
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

func TestAgentStreamClosesWithoutEventWhenContextAlreadyCanceledAndHistoryInvalid(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	provider := &recordingProvider{inner: chatFunc(func(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
		t.Fatal("provider.Chat() should not be called for invalid history")
		return nil, nil
	})}

	a := New(provider, tools.NewRegistry(), "fake-tool-model")
	stream := a.Stream(ctx, []Message{
		AssistantMessage{},
		UserMessage{Content: "continue"},
	})

	select {
	case event, ok := <-stream:
		if ok {
			t.Fatalf("Stream() delivered %T (%#v), want closed stream without event", event, event)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Stream() did not close promptly")
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
	got, err := runAgentResult(t, a, context.Background(), "compute (2 + 3) * 4")
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

func TestAgentRunAgentMessagesForwardsProvidedHistoryToProvider(t *testing.T) {
	history := []Message{
		UserMessage{Content: "what is 2 + 2?"},
		AssistantMessage{Content: "2 + 2 = 4."},
		UserMessage{Content: "multiply that by 3"},
	}
	want := []llms.Message{
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
	got, err := runAgentMessages(t, a, context.Background(), history)
	if err != nil {
		t.Fatalf("RunAgentMessages() error = %v", err)
	}
	if got == nil {
		t.Fatal("RunAgentMessages() result = nil")
	}
	if got.Answer != "4 * 3 = 12" {
		t.Fatalf("RunAgentMessages().Answer = %q", got.Answer)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("chat requests len = %d, want 1", len(provider.requests))
	}

	assertMessagesEqual(t, provider.requests[0].Messages, want)
}

func TestAgentRunAgentMessagesContinuesToolCallingFromProvidedHistory(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})

	history := []Message{
		UserMessage{Content: "what is 4 * 4?"},
		AssistantMessage{Content: "4 * 4 = 16."},
		UserMessage{Content: "now multiply that by 5"},
	}
	wantHistory := []llms.Message{
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
			assertMessagesEqual(t, req.Messages, wantHistory)
			return &llms.ChatResponse{Message: assistantToolCall}, nil
		case 2:
			want := append(append([]llms.Message{}, wantHistory...), assistantToolCall, toolResult)
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
	got, err := runAgentMessages(t, a, context.Background(), history)
	if err != nil {
		t.Fatalf("RunAgentMessages() error = %v", err)
	}
	if got == nil {
		t.Fatal("RunAgentMessages() result = nil")
	}
	if got.Answer != "16 * 5 = 80" {
		t.Fatalf("RunAgentMessages().Answer = %q", got.Answer)
	}
	if got.ToolRounds != 1 {
		t.Fatalf("RunAgentMessages().ToolRounds = %d, want 1", got.ToolRounds)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("RunAgentMessages().Steps len = %d, want 1", len(got.Steps))
	}
	if len(provider.requests) != 2 {
		t.Fatalf("chat requests len = %d, want 2", len(provider.requests))
	}

	step := got.Steps[0]
	if step.ToolCallID != "call_history_mul" {
		t.Fatalf("RunAgentMessages().Steps[0].ToolCallID = %q", step.ToolCallID)
	}
	if step.ToolName != "calculator" {
		t.Fatalf("RunAgentMessages().Steps[0].ToolName = %q", step.ToolName)
	}
	if step.Arguments != `{"a":16,"b":5,"op":"mul"}` {
		t.Fatalf("RunAgentMessages().Steps[0].Arguments = %q", step.Arguments)
	}
	if step.Result != "80" {
		t.Fatalf("RunAgentMessages().Steps[0].Result = %q", step.Result)
	}
	if step.Error != "" {
		t.Fatalf("RunAgentMessages().Steps[0].Error = %q, want empty", step.Error)
	}
}
