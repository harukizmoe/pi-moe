package session

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/tools"
)

func TestNewPromptUsesConfiguredFakeProviderAndCalculator(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)

	s, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	answer := collectSessionAnswer(t, s.Prompt(context.Background(), "use calculator to compute 13 * 7"))
	if answer != "13 * 7 = 91" {
		t.Fatalf("answer = %q, want %q", answer, "13 * 7 = 91")
	}
}

func TestNewUsesProviderNameOverride(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: bad-default
  providers:
    bad-default:
      type: does_not_exist
      model: broken-model
    fake-local:
      type: fake
      model: fake-tool-model
`)

	s, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		ProviderName:       "fake-local",
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	answer := collectSessionAnswer(t, s.Prompt(context.Background(), "use calculator to compute 13 * 7"))
	if answer != "13 * 7 = 91" {
		t.Fatalf("answer = %q, want %q", answer, "13 * 7 = 91")
	}
}

func TestNewReturnsErrorWhenProviderNameMissing(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)

	_, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		ProviderName:       "missing-provider",
		Logger:             logger.NewNoop(),
	})
	if err == nil {
		t.Fatal("New() error = nil, want unknown provider error")
	}
	if !strings.Contains(err.Error(), `unknown provider "missing-provider"`) {
		t.Fatalf("New() error = %v, want unknown provider message", err)
	}
}

func TestNewReturnsErrorWhenDefaultProviderMissing(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: missing-provider
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)

	_, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
	})
	if err == nil {
		t.Fatal("New() error = nil, want unknown provider error")
	}
	if !strings.Contains(err.Error(), `unknown provider "missing-provider"`) {
		t.Fatalf("New() error = %v, want unknown provider message", err)
	}
}

func TestNewReturnsErrorWhenProviderTypeUnknown(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: bad-provider
  providers:
    bad-provider:
      type: does_not_exist
      model: fake-tool-model
`)

	_, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
	})
	if err == nil {
		t.Fatal("New() error = nil, want unknown llm provider type error")
	}
	if !strings.Contains(err.Error(), "unknown llm provider type") {
		t.Fatalf("New() error = %v, want unknown llm provider type message", err)
	}
}

func TestSessionPromptPersistsOnlyTerminalMessages(t *testing.T) {
	s := newFakeSession(t)

	events := collectSessionStreamEvents(t, s.Prompt(context.Background(), "use calculator to compute 13 * 7"))
	assertSessionEventTypes(t, events,
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

	messages := s.Messages()
	if len(messages) != 4 {
		t.Fatalf("Messages() len = %d, want 4: %#v", len(messages), messages)
	}
	if got := messages[0].(agent.UserMessage).Content; got != "use calculator to compute 13 * 7" {
		t.Fatalf("user message content = %q", got)
	}
	assistantWithTool := messages[1].(agent.AssistantMessage)
	if len(assistantWithTool.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls len = %d, want 1", len(assistantWithTool.ToolCalls))
	}
	toolResult := messages[2].(agent.ToolResultMessage)
	if toolResult.ToolCallID != assistantWithTool.ToolCalls[0].ID {
		t.Fatalf("tool result call id = %q, want %q", toolResult.ToolCallID, assistantWithTool.ToolCalls[0].ID)
	}
	if toolResult.Content != "91" {
		t.Fatalf("tool result content = %q, want 91", toolResult.Content)
	}
	finalAssistant := messages[3].(agent.AssistantMessage)
	if finalAssistant.Content != "13 * 7 = 91" {
		t.Fatalf("final assistant content = %q", finalAssistant.Content)
	}
}

func TestSessionMessagesReturnsDefensiveSnapshot(t *testing.T) {
	s := newFakeSession(t)
	collectSessionStreamEvents(t, s.Prompt(context.Background(), "use calculator to compute 13 * 7"))

	first := s.Messages()
	first[0] = agent.UserMessage{Content: "mutated"}
	assistantMessage := first[1].(agent.AssistantMessage)
	assistantMessage.ToolCalls[0].Function.Arguments = "mutated"
	first[1] = assistantMessage

	second := s.Messages()
	if got := second[0].(agent.UserMessage).Content; got != "use calculator to compute 13 * 7" {
		t.Fatalf("Messages()[0].Content after caller mutation = %q", got)
	}
	secondAssistant := second[1].(agent.AssistantMessage)
	if got := secondAssistant.ToolCalls[0].Function.Arguments; got != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("Messages()[1].ToolCalls[0].Arguments after caller mutation = %q", got)
	}
}

func TestSessionPromptRejectsEmptyInputWithoutTranscriptMutation(t *testing.T) {
	s := newFakeSession(t)

	events := collectSessionStreamEvents(t, s.Prompt(context.Background(), " \n\t "))
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	errEvent, ok := events[0].(ErrorEvent)
	if !ok {
		t.Fatalf("event[0] = %T, want ErrorEvent", events[0])
	}
	if errEvent.Error == nil || !strings.Contains(errEvent.Error.Error(), "empty input") {
		t.Fatalf("ErrorEvent.Error = %v, want empty input", errEvent.Error)
	}
	if messages := s.Messages(); len(messages) != 0 {
		t.Fatalf("Messages() len = %d, want 0", len(messages))
	}
}

func TestSessionPromptRejectsConcurrentPromptWithoutTranscriptMutation(t *testing.T) {
	provider := newBlockingProvider()
	s := &Session{agent: agent.New(provider, tools.NewRegistry(), "blocking-model"), listeners: make(map[chan Event]struct{})}

	first := s.Prompt(context.Background(), "first prompt")
	firstDone := make(chan []Event, 1)
	go func() {
		firstDone <- collectSessionStreamEvents(t, first)
	}()
	<-provider.started

	secondEvents := collectSessionStreamEvents(t, s.Prompt(context.Background(), "second prompt"))
	if len(secondEvents) != 1 {
		t.Fatalf("second prompt events len = %d, want 1", len(secondEvents))
	}
	errEvent, ok := secondEvents[0].(ErrorEvent)
	if !ok {
		t.Fatalf("second event = %T, want ErrorEvent", secondEvents[0])
	}
	if errEvent.Error == nil || !strings.Contains(errEvent.Error.Error(), "active turn") {
		t.Fatalf("second ErrorEvent.Error = %v, want active turn", errEvent.Error)
	}

	messagesDuringFirstPrompt := s.Messages()
	if len(messagesDuringFirstPrompt) != 1 {
		t.Fatalf("Messages() len while first prompt active = %d, want 1", len(messagesDuringFirstPrompt))
	}
	if got := messagesDuringFirstPrompt[0].(agent.UserMessage).Content; got != "first prompt" {
		t.Fatalf("active user message content = %q", got)
	}

	close(provider.release)
	<-firstDone

	messagesAfterFirstPrompt := s.Messages()
	if len(messagesAfterFirstPrompt) != 2 {
		t.Fatalf("Messages() len after first prompt = %d, want 2", len(messagesAfterFirstPrompt))
	}
	if got := messagesAfterFirstPrompt[0].(agent.UserMessage).Content; got != "first prompt" {
		t.Fatalf("final user message content = %q", got)
	}
}

func TestSessionCancelEmitsTerminalErrorEvent(t *testing.T) {
	provider := newBlockingProvider()
	s := &Session{agent: agent.New(provider, tools.NewRegistry(), "blocking-model"), listeners: make(map[chan Event]struct{})}

	stream := s.Prompt(context.Background(), "first prompt")
	eventsDone := make(chan []Event, 1)
	go func() {
		eventsDone <- collectSessionStreamEvents(t, stream)
	}()

	<-provider.started
	s.Cancel()

	var events []Event
	select {
	case events = <-eventsDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Prompt() did not terminate after Cancel()")
	}

	if len(events) == 0 {
		t.Fatal("events len = 0, want terminal ErrorEvent")
	}

	errEvent, ok := events[len(events)-1].(ErrorEvent)
	if !ok {
		t.Fatalf("last event = %T, want ErrorEvent", events[len(events)-1])
	}
	if errEvent.Error == nil || !strings.Contains(errEvent.Error.Error(), context.Canceled.Error()) {
		t.Fatalf("last ErrorEvent.Error = %v, want context cancellation", errEvent.Error)
	}

	for i, event := range events {
		if _, ok := event.(RunEndEvent); ok {
			t.Fatalf("event[%d] = %T, want cancellation stream without RunEndEvent", i, event)
		}
	}
}

func TestSessionPromptDoesNotPersistOverLimitToolCallMessage(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})
	provider := &maxStepLoopProvider{t: t}
	s := &Session{agent: agent.NewWithOptions(provider, registry, "fake-tool-model", agent.Options{MaxSteps: 1}), listeners: make(map[chan Event]struct{})}

	events := collectSessionStreamEvents(t, s.Prompt(context.Background(), "compute (2 + 3) * 4"))
	errEvent, ok := events[len(events)-1].(ErrorEvent)
	if !ok {
		t.Fatalf("last event = %T, want ErrorEvent", events[len(events)-1])
	}
	if errEvent.Error == nil || !strings.Contains(errEvent.Error.Error(), "max steps") {
		t.Fatalf("ErrorEvent.Error = %v, want max steps", errEvent.Error)
	}

	messages := s.Messages()
	if len(messages) != 3 {
		t.Fatalf("Messages() len = %d, want 3 without dangling over-limit assistant: %#v", len(messages), messages)
	}
	assistantWithTool := messages[1].(agent.AssistantMessage)
	if len(assistantWithTool.ToolCalls) != 1 {
		t.Fatalf("first assistant tool calls len = %d, want 1", len(assistantWithTool.ToolCalls))
	}
	if _, ok := messages[2].(agent.ToolResultMessage); !ok {
		t.Fatalf("Messages()[2] = %T, want ToolResultMessage", messages[2])
	}
}

func newFakeSession(t *testing.T) *Session {
	t.Helper()

	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)

	s, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return s
}

func collectSessionAnswer(t *testing.T, stream <-chan Event) string {
	t.Helper()

	var answer string
	for event := range stream {
		switch event := event.(type) {
		case MessageEndEvent:
			if len(event.Message.ToolCalls) == 0 {
				answer = event.Message.Content
			}
		case ErrorEvent:
			if event.Error != nil {
				t.Fatalf("stream error = %v", event.Error)
			}
		}
	}
	return answer
}

func writeProvidersConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "providers.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write providers config: %v", err)
	}
	return path
}

func sessionChatStream(message llms.Message) <-chan llms.ChatStreamEvent {
	events := make([]llms.ChatStreamEvent, 0, len(message.ToolCalls)+2)
	if message.Content != "" {
		events = append(events, llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Role: message.Role, Content: message.Content}})
	}
	for i, call := range message.ToolCalls {
		events = append(events, llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDelta, Delta: llms.ChatStreamDelta{Role: message.Role, ToolCalls: []llms.ToolCallDelta{{
			Index: i,
			ID:    call.ID,
			Type:  call.Type,
			Function: llms.ToolCallFunctionDelta{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		}}}})
	}
	events = append(events, llms.ChatStreamEvent{Type: llms.ChatStreamEventTypeDone, Message: message})
	stream := make(chan llms.ChatStreamEvent, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return stream
}

type maxStepLoopProvider struct {
	t     *testing.T
	round int
}

func (p *maxStepLoopProvider) ChatStream(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
	p.t.Helper()
	p.round++
	switch p.round {
	case 1:
		return sessionChatStream(llms.Message{
			Role: llms.RoleAssistant,
			ToolCalls: []llms.ToolCall{
				maxStepCalculatorToolCall("call_step_1", `{"a":2,"b":3,"op":"add"}`),
			},
		}), nil
	case 2:
		last := req.Messages[len(req.Messages)-1]
		if last.Role != llms.RoleTool || last.Content != "5" {
			p.t.Fatalf("second request last message = %#v, want tool result 5", last)
		}
		return sessionChatStream(llms.Message{
			Role: llms.RoleAssistant,
			ToolCalls: []llms.ToolCall{
				maxStepCalculatorToolCall("call_step_2", `{"a":5,"b":4,"op":"mul"}`),
			},
		}), nil
	default:
		p.t.Fatalf("unexpected chat round = %d", p.round)
		return nil, nil
	}
}

func maxStepCalculatorToolCall(id string, arguments string) llms.ToolCall {
	return llms.ToolCall{
		ID:   id,
		Type: "function",
		Function: llms.ToolCallFunction{
			Name:      "calculator",
			Arguments: arguments,
		},
	}
}

type blockingProvider struct {
	started chan struct{}
	release chan struct{}
}

func newBlockingProvider() *blockingProvider {
	return &blockingProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (p *blockingProvider) ChatStream(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	select {
	case <-p.release:
		return sessionChatStream(llms.Message{Role: llms.RoleAssistant, Content: "first done"}), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func assertSessionEventTypes(t *testing.T, events []Event, want ...Event) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("events len = %d, want %d: %#v", len(events), len(want), events)
	}
	for i := range want {
		if gotType, wantType := reflect.TypeOf(events[i]), reflect.TypeOf(want[i]); gotType != wantType {
			t.Fatalf("event[%d] = %T, want %T", i, events[i], want[i])
		}
	}
}
