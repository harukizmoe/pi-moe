package session

import (
	"context"
	"errors"
	"fmt"
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
		RunCompletedEvent{},
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

func TestSessionOpenRestoresJSONLBackedTranscript(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")

	first, err := Open(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	}, sessionPath)
	if err != nil {
		t.Fatalf("Open() first error = %v", err)
	}
	if answer := collectSessionAnswer(t, first.Prompt(context.Background(), "use calculator to compute 13 * 7")); answer != "13 * 7 = 91" {
		t.Fatalf("answer = %q, want %q", answer, "13 * 7 = 91")
	}
	want := first.Messages()

	reopened, err := Open(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	}, sessionPath)
	if err != nil {
		t.Fatalf("Open() reopened error = %v", err)
	}

	got := reopened.Messages()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened Messages() = %#v, want %#v", got, want)
	}
	if len(got) != 4 {
		t.Fatalf("reopened Messages() len = %d, want 4 terminal transcript facts: %#v", len(got), got)
	}
	if got[0].(agent.UserMessage).Content != "use calculator to compute 13 * 7" {
		t.Fatalf("reopened user message = %#v", got[0])
	}
	if got[3].(agent.AssistantMessage).Content != "13 * 7 = 91" {
		t.Fatalf("reopened final assistant message = %#v", got[3])
	}
}

func TestSessionOpenReturnsErrorForMalformedJSONL(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatalf("write malformed session file: %v", err)
	}

	_, err := Open(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	}, sessionPath)
	if err == nil {
		t.Fatal("Open() error = nil, want malformed session file error")
	}

	errText := err.Error()
	if !strings.Contains(errText, "parse session file") {
		t.Fatalf("Open() error = %q, want session parse context", errText)
	}
	if !strings.Contains(errText, sessionPath) && !strings.Contains(errText, "line 1") && !strings.Contains(errText, "invalid character") {
		t.Fatalf("Open() error = %q, want path, line, or JSON decoder context", errText)
	}
}

func TestSessionOpenDoesNotDurablyPersistCanceledPrompt(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")

	s, err := Open(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	}, sessionPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	provider := newBlockingProvider()
	s.runtime = agent.NewRuntime(provider, tools.NewRegistry(), "blocking-model", agent.Options{})

	stream := s.Prompt(context.Background(), "prompt that will be canceled")
	eventsDone := make(chan []Event, 1)
	go func() {
		eventsDone <- collectSessionStreamEvents(t, stream)
	}()

	<-provider.started
	s.Cancel()
	select {
	case <-eventsDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Prompt() did not terminate after Cancel()")
	}

	reopened, err := Open(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	}, sessionPath)
	if err != nil {
		t.Fatalf("Open() reopened error = %v", err)
	}
	if messages := reopened.Messages(); len(messages) != 0 {
		t.Fatalf("reopened Messages() len after canceled prompt = %d, want 0: %#v", len(messages), messages)
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
	s := &Session{runtime: agent.NewRuntime(provider, tools.NewRegistry(), "blocking-model", agent.Options{})}

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
	s := &Session{runtime: agent.NewRuntime(provider, tools.NewRegistry(), "blocking-model", agent.Options{})}

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
		t.Fatal("events len = 0, want terminal RunCanceledEvent")
	}

	canceled, ok := events[len(events)-1].(RunCanceledEvent)
	if !ok {
		t.Fatalf("last event = %T, want RunCanceledEvent", events[len(events)-1])
	}
	if canceled.Error == nil || !strings.Contains(canceled.Error.Error(), context.Canceled.Error()) {
		t.Fatalf("RunCanceledEvent.Error = %v, want context cancellation", canceled.Error)
	}

	for i, event := range events {
		if _, ok := event.(RunCompletedEvent); ok {
			t.Fatalf("event[%d] = %T, want cancellation stream without RunCompletedEvent", i, event)
		}
	}
}

func TestSessionPromptDoesNotPersistOverLimitToolCallMessage(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})
	provider := &maxStepLoopProvider{t: t}
	s := &Session{runtime: agent.NewRuntime(provider, registry, "fake-tool-model", agent.Options{MaxSteps: 1})}

	events := collectSessionStreamEvents(t, s.Prompt(context.Background(), "compute (2 + 3) * 4"))
	failed, ok := events[len(events)-1].(RunFailedEvent)
	if !ok {
		t.Fatalf("last event = %T, want RunFailedEvent", events[len(events)-1])
	}
	if failed.Error == nil || !strings.Contains(failed.Error.Error(), "max steps") {
		t.Fatalf("RunFailedEvent.Error = %v, want max steps", failed.Error)
	}

	messages := s.Messages()
	if len(messages) != 0 {
		t.Fatalf("Messages() len = %d, want 0 after failed run rollback: %#v", len(messages), messages)
	}
}

func TestSessionPromptDoesNotDurablyPersistFailedRunWithoutRunEnd(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})
	provider := &maxStepLoopProvider{t: t}
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	s := &Session{
		runtime: agent.NewRuntime(provider, registry, "fake-tool-model", agent.Options{MaxSteps: 1}),
		store:   newFileStore(sessionPath),
	}

	events := collectSessionStreamEvents(t, s.Prompt(context.Background(), "compute (2 + 3) * 4"))
	if _, ok := events[len(events)-1].(RunFailedEvent); !ok {
		t.Fatalf("last event = %T, want RunFailedEvent", events[len(events)-1])
	}

	reopenedMessages, err := LoadMessages(sessionPath)
	if err != nil {
		t.Fatalf("LoadMessages() error = %v", err)
	}
	if len(reopenedMessages) != 0 {
		t.Fatalf("LoadMessages() len after failed run = %d, want 0: %#v", len(reopenedMessages), reopenedMessages)
	}
}

func TestSessionCommitsMemoryCandidatesOnlyAfterCompletedRun(t *testing.T) {
	provider := sessionProviderFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return sessionChatStream(llms.Message{Role: llms.RoleAssistant, Content: "done"}), nil
	})
	extractor := sessionMemoryExtractorFunc(func(context.Context, agent.MemoryExtractionInput) ([]agent.MemoryCandidate, error) {
		return []agent.MemoryCandidate{{Operation: agent.MemoryOperationUpsert, Key: "profile/name", Content: "Ada", Source: "run-output", Scope: "user:test", Provenance: "explicit user statement"}}, nil
	})
	runtime := agent.NewRuntimeWithOptions(provider, "fake-model", agent.Options{}, agent.RunRequestOptions{MemoryExtractor: extractor})
	s, err := NewWithRuntime(runtime)
	if err != nil {
		t.Fatalf("NewWithRuntime() error = %v", err)
	}

	events := collectSessionStreamEvents(t, s.Prompt(context.Background(), "remember my name"))
	for _, event := range events {
		if _, ok := event.(MemoryCandidateEvent); ok {
			t.Fatalf("uncommitted candidate leaked through Session stream: %#v", events)
		}
	}
	candidates := s.MemoryCandidates()
	if len(candidates) != 1 || candidates[0].Key != "profile/name" {
		t.Fatalf("MemoryCandidates() = %#v, want committed candidate", candidates)
	}
	candidates[0].Key = "mutated"
	if got := s.MemoryCandidates()[0].Key; got != "profile/name" {
		t.Fatalf("MemoryCandidates() retained caller mutation %q", got)
	}
}

func TestSessionDoesNotCommitMemoryCandidatesFromFailedOrCanceledRuns(t *testing.T) {
	for _, mode := range []string{"failed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			extractorCalled := false
			extractor := sessionMemoryExtractorFunc(func(context.Context, agent.MemoryExtractionInput) ([]agent.MemoryCandidate, error) {
				extractorCalled = true
				return []agent.MemoryCandidate{{Operation: agent.MemoryOperationUpsert, Key: "profile/name", Content: "Ada", Source: "run-output", Scope: "user:test", Provenance: "explicit user statement"}}, nil
			})
			started := make(chan struct{})
			provider := sessionProviderFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
				if mode == "failed" {
					return nil, errors.New("provider failed")
				}
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			})
			runtime := agent.NewRuntimeWithOptions(provider, "fake-model", agent.Options{}, agent.RunRequestOptions{MemoryExtractor: extractor})
			s, err := NewWithRuntime(runtime)
			if err != nil {
				t.Fatalf("NewWithRuntime() error = %v", err)
			}

			var events []Event
			if mode == "failed" {
				events = collectSessionStreamEvents(t, s.Prompt(context.Background(), "remember my name"))
			} else {
				eventsDone := make(chan []Event, 1)
				go func() {
					eventsDone <- collectSessionStreamEvents(t, s.Prompt(context.Background(), "remember my name"))
				}()
				<-started
				s.Cancel()
				select {
				case events = <-eventsDone:
				case <-time.After(200 * time.Millisecond):
					t.Fatal("canceled memory run did not terminate")
				}
			}
			if len(events) == 0 {
				t.Fatal("events len = 0, want terminal event")
			}
			if mode == "failed" {
				if _, ok := events[len(events)-1].(RunFailedEvent); !ok {
					t.Fatalf("last event = %T, want RunFailedEvent", events[len(events)-1])
				}
			} else if _, ok := events[len(events)-1].(RunCanceledEvent); !ok {
				t.Fatalf("last event = %T, want RunCanceledEvent", events[len(events)-1])
			}
			if extractorCalled {
				t.Fatal("memory extractor called before a completed Run")
			}
			if candidates := s.MemoryCandidates(); len(candidates) != 0 {
				t.Fatalf("MemoryCandidates() after %s Run = %#v, want none", mode, candidates)
			}
		})
	}
}

func TestSessionRejectsPendingMemoryCandidatesWhenTranscriptCommitFails(t *testing.T) {
	provider := sessionProviderFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return sessionChatStream(llms.Message{Role: llms.RoleAssistant, Content: "done"}), nil
	})
	extractor := sessionMemoryExtractorFunc(func(context.Context, agent.MemoryExtractionInput) ([]agent.MemoryCandidate, error) {
		return []agent.MemoryCandidate{{Operation: agent.MemoryOperationUpsert, Key: "profile/name", Content: "Ada", Source: "run-output", Scope: "user:test", Provenance: "explicit user statement"}}, nil
	})
	s := &Session{
		runtime: agent.NewRuntimeWithOptions(provider, "fake-model", agent.Options{}, agent.RunRequestOptions{MemoryExtractor: extractor}),
		store:   newFileStore(t.TempDir()),
	}

	events := collectSessionStreamEvents(t, s.Prompt(context.Background(), "remember my name"))
	if _, ok := events[len(events)-1].(RunFailedEvent); !ok {
		t.Fatalf("last event = %T, want Session commit RunFailedEvent", events[len(events)-1])
	}
	if candidates := s.MemoryCandidates(); len(candidates) != 0 {
		t.Fatalf("MemoryCandidates() after failed Session commit = %#v, want none", candidates)
	}
	if messages := s.Messages(); len(messages) != 0 {
		t.Fatalf("Messages() after failed Session commit = %#v, want rollback", messages)
	}
}

func TestSessionPersistsAcceptedContextSummaryAndReusesItAfterReopen(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	providerCalls := 0
	provider := sessionProviderFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		providerCalls++
		return sessionChatStream(llms.Message{Role: llms.RoleAssistant, Content: fmt.Sprintf("answer-%d", providerCalls)}), nil
	})
	compactCalls := 0
	compactor := sessionCompactorFunc(func(ctx context.Context, input agent.ContextCompactionInput) (agent.ContextSummary, error) {
		compactCalls++
		if input.ExistingSummary != nil || len(input.Messages) != 2 {
			t.Fatalf("compaction input = %#v, want first completed turn without an existing summary", input)
		}
		return agent.ContextSummary{ID: "summary-1", Content: "first turn summary"}, nil
	})
	runtime := agent.NewRuntime(provider, tools.NewRegistry(), "fake-model", agent.Options{Context: agent.ContextOptions{
		ContextWindow: 25,
		Estimator:     sessionFixedMessageEstimator{},
		Compactor:     compactor,
	}})
	s, err := OpenWithRuntime(runtime, sessionPath)
	if err != nil {
		t.Fatalf("OpenWithRuntime() error = %v", err)
	}
	collectSessionStreamEvents(t, s.Prompt(context.Background(), "first question"))
	secondEvents := collectSessionStreamEvents(t, s.Prompt(context.Background(), "second question"))
	if compactCalls != 1 {
		t.Fatalf("compactor calls = %d, want 1", compactCalls)
	}
	for _, event := range secondEvents {
		if _, ok := event.(ContextSummaryCandidateEvent); ok {
			t.Fatalf("uncommitted summary candidate leaked through Session stream: %#v", secondEvents)
		}
	}

	state, err := loadSessionState(sessionPath)
	if err != nil {
		t.Fatalf("loadSessionState() error = %v", err)
	}
	if state.contextSummary == nil || state.contextSummary.ID != "summary-1" || state.contextSummary.Content != "first turn summary" || state.summarizedMessages != 2 {
		t.Fatalf("persisted context state = %#v, want accepted first-turn summary", state)
	}
	if len(state.messages) != 4 {
		t.Fatalf("persisted transcript len = %d, want full four-message history", len(state.messages))
	}

	var reopenedRequest llms.ChatRequest
	reopenedProvider := sessionProviderFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		reopenedRequest = req
		return sessionChatStream(llms.Message{Role: llms.RoleAssistant, Content: "answer-3"}), nil
	})
	reopenedRuntime := agent.NewRuntime(reopenedProvider, tools.NewRegistry(), "fake-model", agent.Options{Context: agent.ContextOptions{
		ContextWindow: 45,
		Estimator:     sessionFixedMessageEstimator{},
	}})
	reopened, err := OpenWithRuntime(reopenedRuntime, sessionPath)
	if err != nil {
		t.Fatalf("OpenWithRuntime() reopened error = %v", err)
	}
	collectSessionStreamEvents(t, reopened.Prompt(context.Background(), "third question"))
	if len(reopenedRequest.Messages) != 4 {
		t.Fatalf("reopened provider messages = %#v, want summary, second turn, and current question", reopenedRequest.Messages)
	}
	if !strings.Contains(reopenedRequest.Messages[0].Content, "first turn summary") || !strings.Contains(reopenedRequest.Messages[0].Content, "untrusted historical data") {
		t.Fatalf("reopened summary message = %#v, want explicitly untrusted accepted summary", reopenedRequest.Messages[0])
	}
	if reopenedRequest.Messages[1].Content != "second question" || reopenedRequest.Messages[2].Content != "answer-2" || reopenedRequest.Messages[3].Content != "third question" {
		t.Fatalf("reopened provider messages = %#v, want unsummarized second turn and current question", reopenedRequest.Messages)
	}
	for _, message := range reopenedRequest.Messages {
		if strings.Contains(message.Content, "first question") || strings.Contains(message.Content, "answer-1") {
			t.Fatalf("reopened provider request repeated summarized transcript: %#v", reopenedRequest.Messages)
		}
	}
}

func TestSessionRejectsContextSummaryFromFailedAndCanceledRuns(t *testing.T) {
	for _, mode := range []string{"failed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
			providerCalls := 0
			started := make(chan struct{})
			provider := sessionProviderFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
				providerCalls++
				if providerCalls == 1 {
					return sessionChatStream(llms.Message{Role: llms.RoleAssistant, Content: "first answer"}), nil
				}
				if mode == "failed" {
					return nil, errors.New("provider failed after compaction")
				}
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			})
			compactor := sessionCompactorFunc(func(ctx context.Context, input agent.ContextCompactionInput) (agent.ContextSummary, error) {
				return agent.ContextSummary{ID: "rejected-summary", Content: "must not persist"}, nil
			})
			runtime := agent.NewRuntime(provider, tools.NewRegistry(), "fake-model", agent.Options{Context: agent.ContextOptions{
				ContextWindow: 25,
				Estimator:     sessionFixedMessageEstimator{},
				Compactor:     compactor,
			}})
			s, err := OpenWithRuntime(runtime, sessionPath)
			if err != nil {
				t.Fatalf("OpenWithRuntime() error = %v", err)
			}
			collectSessionStreamEvents(t, s.Prompt(context.Background(), "first question"))

			var secondEvents []Event
			if mode == "failed" {
				secondEvents = collectSessionStreamEvents(t, s.Prompt(context.Background(), "second question"))
			} else {
				eventsDone := make(chan []Event, 1)
				go func() {
					eventsDone <- collectSessionStreamEvents(t, s.Prompt(context.Background(), "second question"))
				}()
				<-started
				s.Cancel()
				select {
				case secondEvents = <-eventsDone:
				case <-time.After(200 * time.Millisecond):
					t.Fatal("canceled compacted prompt did not terminate")
				}
			}
			if len(secondEvents) == 0 {
				t.Fatal("second prompt emitted no terminal event")
			}
			if mode == "failed" {
				if _, ok := secondEvents[len(secondEvents)-1].(RunFailedEvent); !ok {
					t.Fatalf("last event = %T, want RunFailedEvent", secondEvents[len(secondEvents)-1])
				}
			} else if _, ok := secondEvents[len(secondEvents)-1].(RunCanceledEvent); !ok {
				t.Fatalf("last event = %T, want RunCanceledEvent", secondEvents[len(secondEvents)-1])
			}

			state, err := loadSessionState(sessionPath)
			if err != nil {
				t.Fatalf("loadSessionState() error = %v", err)
			}
			if state.contextSummary != nil || state.summarizedMessages != 0 || len(state.messages) != 2 {
				t.Fatalf("state after %s compacted run = %#v, want only first completed turn", mode, state)
			}

			var reopenedRequest llms.ChatRequest
			inspector := sessionProviderFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
				reopenedRequest = req
				return sessionChatStream(llms.Message{Role: llms.RoleAssistant, Content: "third answer"}), nil
			})
			reopenedRuntime := agent.NewRuntime(inspector, tools.NewRegistry(), "fake-model", agent.Options{Context: agent.ContextOptions{
				ContextWindow: 35,
				Estimator:     sessionFixedMessageEstimator{},
			}})
			reopened, err := OpenWithRuntime(reopenedRuntime, sessionPath)
			if err != nil {
				t.Fatalf("OpenWithRuntime() reopened error = %v", err)
			}
			collectSessionStreamEvents(t, reopened.Prompt(context.Background(), "third question"))
			if len(reopenedRequest.Messages) != 3 || reopenedRequest.Messages[0].Content != "first question" || reopenedRequest.Messages[1].Content != "first answer" || reopenedRequest.Messages[2].Content != "third question" {
				t.Fatalf("provider request after rejected summary = %#v, want original completed turn and current question", reopenedRequest.Messages)
			}
		})
	}
}

type sessionFixedMessageEstimator struct{}

func (sessionFixedMessageEstimator) Estimate(ctx context.Context, input agent.ContextEstimateInput) (agent.TokenEstimate, error) {
	return agent.TokenEstimate{Tokens: len(input.Messages) * 10, Estimator: "session-fixed-test"}, nil
}

type sessionCompactorFunc func(context.Context, agent.ContextCompactionInput) (agent.ContextSummary, error)

func (f sessionCompactorFunc) Compact(ctx context.Context, input agent.ContextCompactionInput) (agent.ContextSummary, error) {
	return f(ctx, input)
}

type sessionProviderFunc func(context.Context, llms.ChatRequest) (<-chan llms.ChatStreamEvent, error)

func (f sessionProviderFunc) ChatStream(ctx context.Context, request llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
	return f(ctx, request)
}

type sessionMemoryExtractorFunc func(context.Context, agent.MemoryExtractionInput) ([]agent.MemoryCandidate, error)

func (f sessionMemoryExtractorFunc) Extract(ctx context.Context, input agent.MemoryExtractionInput) ([]agent.MemoryCandidate, error) {
	return f(ctx, input)
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

type Config = agent.Config

func New(ctx context.Context, cfg Config) (*Session, error) {
	runtime, err := agent.NewConfiguredRuntime(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return NewWithRuntime(runtime)
}

func Open(ctx context.Context, cfg Config, path string) (*Session, error) {
	runtime, err := agent.NewConfiguredRuntime(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return OpenWithRuntime(runtime, path)
}
