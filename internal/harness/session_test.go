package harness

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

func TestSessionPromptPersistsOnlyTerminalMessages(t *testing.T) {
	h := newFakeHarness(t)
	session := h.NewSession()

	events := collectHarnessStreamEvents(t, session.Prompt(context.Background(), "use calculator to compute 13 * 7"))
	assertHarnessEventTypes(t, events,
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

	messages := session.Messages()
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
	h := newFakeHarness(t)
	session := h.NewSession()
	collectHarnessStreamEvents(t, session.Prompt(context.Background(), "use calculator to compute 13 * 7"))

	first := session.Messages()
	first[0] = agent.UserMessage{Content: "mutated"}
	assistantMessage := first[1].(agent.AssistantMessage)
	assistantMessage.ToolCalls[0].Function.Arguments = "mutated"
	first[1] = assistantMessage

	second := session.Messages()
	if got := second[0].(agent.UserMessage).Content; got != "use calculator to compute 13 * 7" {
		t.Fatalf("Messages()[0].Content after caller mutation = %q", got)
	}
	secondAssistant := second[1].(agent.AssistantMessage)
	if got := secondAssistant.ToolCalls[0].Function.Arguments; got != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("Messages()[1].ToolCalls[0].Arguments after caller mutation = %q", got)
	}
}

func TestSessionPromptRejectsEmptyInputWithoutTranscriptMutation(t *testing.T) {
	h := newFakeHarness(t)
	session := h.NewSession()

	events := collectHarnessStreamEvents(t, session.Prompt(context.Background(), " \n\t "))
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
	if messages := session.Messages(); len(messages) != 0 {
		t.Fatalf("Messages() len = %d, want 0", len(messages))
	}
}

func TestSessionPromptRejectsConcurrentPromptWithoutTranscriptMutation(t *testing.T) {
	provider := newBlockingProvider()
	h := &Harness{agent: agent.New(provider, tools.NewRegistry(), "blocking-model")}
	session := h.NewSession()

	first := session.Prompt(context.Background(), "first prompt")
	firstDone := make(chan []Event, 1)
	go func() {
		firstDone <- collectHarnessStreamEvents(t, first)
	}()
	<-provider.started

	secondEvents := collectHarnessStreamEvents(t, session.Prompt(context.Background(), "second prompt"))
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

	messagesDuringFirstPrompt := session.Messages()
	if len(messagesDuringFirstPrompt) != 1 {
		t.Fatalf("Messages() len while first prompt active = %d, want 1", len(messagesDuringFirstPrompt))
	}
	if got := messagesDuringFirstPrompt[0].(agent.UserMessage).Content; got != "first prompt" {
		t.Fatalf("active user message content = %q", got)
	}

	close(provider.release)
	<-firstDone

	messagesAfterFirstPrompt := session.Messages()
	if len(messagesAfterFirstPrompt) != 2 {
		t.Fatalf("Messages() len after first prompt = %d, want 2", len(messagesAfterFirstPrompt))
	}
	if got := messagesAfterFirstPrompt[0].(agent.UserMessage).Content; got != "first prompt" {
		t.Fatalf("final user message content = %q", got)
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

func (p *blockingProvider) Chat(ctx context.Context, req llms.ChatRequest) (*llms.ChatResponse, error) {
	select {
	case <-p.started:
	default:
		close(p.started)
	}
	select {
	case <-p.release:
		return &llms.ChatResponse{Message: llms.Message{Role: llms.RoleAssistant, Content: "first done"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func assertHarnessEventTypes(t *testing.T, events []Event, want ...Event) {
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
