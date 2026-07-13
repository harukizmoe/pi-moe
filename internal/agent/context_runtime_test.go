package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

type fixedMessageEstimator struct {
	calls int
}

func (e *fixedMessageEstimator) Estimate(ctx context.Context, input ContextEstimateInput) (TokenEstimate, error) {
	e.calls++
	return TokenEstimate{Tokens: len(input.Messages) * 10, Estimator: "fixed-test", Approximate: false}, nil
}

type compactorFunc func(context.Context, ContextCompactionInput) (ContextSummary, error)

func (f compactorFunc) Compact(ctx context.Context, input ContextCompactionInput) (ContextSummary, error) {
	return f(ctx, input)
}

type summaryAwareEstimator struct{}

func (summaryAwareEstimator) Estimate(ctx context.Context, input ContextEstimateInput) (TokenEstimate, error) {
	tokens := len(input.Messages) * 10
	for _, message := range input.Messages {
		if strings.HasPrefix(message.Content, "Earlier conversation summary") {
			if _, content, ok := strings.Cut(message.Content, "\n"); ok {
				tokens += len(content)
			}
		}
	}
	return TokenEstimate{Tokens: tokens, Estimator: "summary-aware-test"}, nil
}

type tokenEstimatorFunc func(context.Context, ContextEstimateInput) (TokenEstimate, error)

func (f tokenEstimatorFunc) Estimate(ctx context.Context, input ContextEstimateInput) (TokenEstimate, error) {
	return f(ctx, input)
}

func TestRuntimeContextPrunesOnlyCompleteOldTurns(t *testing.T) {
	estimator := &fixedMessageEstimator{}
	recorder := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done"})), nil
	})}
	runtime := NewRuntime(recorder, tools.NewRegistry(), "fake-model", Options{Context: ContextOptions{
		ContextWindow: 30,
		Estimator:     estimator,
	}})
	request := NewRunRequest([]Message{
		UserMessage{Content: "oldest secret"},
		AssistantMessage{Content: "oldest answer"},
		UserMessage{Content: "recent question"},
		AssistantMessage{Content: "recent answer"},
		UserMessage{Content: "current question"},
	})

	events := collectStreamEvents(t, runtime.Run(context.Background(), request))
	if _, ok := events[len(events)-1].(RunCompletedEvent); !ok {
		t.Fatalf("last event = %T, want RunCompletedEvent", events[len(events)-1])
	}
	if len(recorder.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(recorder.requests))
	}
	got := recorder.requests[0].Messages
	if len(got) != 3 {
		t.Fatalf("provider messages = %d, want one retained history turn plus current input", len(got))
	}
	if got[0].Content != "recent question" || got[2].Content != "current question" {
		t.Fatalf("provider messages = %#v, want complete recent turn and current input", got)
	}
	metadata := findContextPreparedEvent(t, events)
	if metadata.PrunedTurns != 1 || metadata.Compacted {
		t.Fatalf("context metadata = %#v, want one pruned turn without compaction", metadata)
	}
	if strings.Contains(fmt.Sprintf("%#v", metadata), "oldest secret") {
		t.Fatal("context metadata leaked pruned message content")
	}
}

func TestRuntimeContextCompactsAtMostOnceAfterPruning(t *testing.T) {
	estimator := summaryAwareEstimator{}
	compactCalls := 0
	compactor := compactorFunc(func(ctx context.Context, input ContextCompactionInput) (ContextSummary, error) {
		compactCalls++
		if len(input.Messages) != 2 {
			t.Fatalf("compactor messages = %d, want one complete old turn", len(input.Messages))
		}
		if input.TargetTokens != 5 {
			t.Fatalf("compactor target = %d, want 5 content tokens after mandatory and envelope costs", input.TargetTokens)
		}
		return ContextSummary{ID: "summary-1", Content: "abcde"}, nil
	})
	recorder := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done"})), nil
	})}
	runtime := NewRuntime(recorder, tools.NewRegistry(), "fake-model", Options{
		BaseSystemPrompt: "trusted runtime instructions",
		Context: ContextOptions{
			ContextWindow: 35,
			Estimator:     estimator,
			Compactor:     compactor,
		},
	})
	request := NewRunRequest([]Message{
		UserMessage{Content: "old question"},
		AssistantMessage{Content: "old answer"},
		UserMessage{Content: "current question"},
	})

	events := collectStreamEvents(t, runtime.Run(context.Background(), request))
	if compactCalls != 1 {
		t.Fatalf("compactor calls = %d, want exactly 1", compactCalls)
	}
	if _, ok := events[len(events)-1].(RunCompletedEvent); !ok {
		t.Fatalf("last event = %T, want RunCompletedEvent", events[len(events)-1])
	}
	providerMessages := recorder.requests[0].Messages
	if len(providerMessages) != 3 || providerMessages[0].Role != llms.RoleSystem || providerMessages[0].Content != "trusted runtime instructions" {
		t.Fatalf("provider messages = %#v, want preserved system instructions before summary and current input", providerMessages)
	}
	if providerMessages[1].Role != llms.RoleUser || !strings.Contains(providerMessages[1].Content, "untrusted historical data") {
		t.Fatalf("summary message = %#v, want explicitly untrusted lower-priority data", providerMessages[1])
	}
	metadata := findContextPreparedEvent(t, events)
	if !metadata.Compacted || metadata.SummaryID != "summary-1" {
		t.Fatalf("context metadata = %#v, want summary reference", metadata)
	}
	if strings.Contains(fmt.Sprintf("%#v", metadata), "abcde") {
		t.Fatal("context metadata leaked summary content")
	}
	var candidate *ContextSummaryCandidateEvent
	for i := range events {
		if event, ok := events[i].(ContextSummaryCandidateEvent); ok {
			candidate = &event
			break
		}
	}
	if candidate == nil {
		t.Fatalf("events = %#v, want ContextSummaryCandidateEvent", events)
	}
	if candidate.RunID == "" || candidate.Candidate.Summary.ID != "summary-1" || candidate.Candidate.Summary.Content != "abcde" || candidate.Candidate.ReplacedMessages != 2 {
		t.Fatalf("summary candidate = %#v, want complete compacted prefix", candidate)
	}
}

func TestRuntimeContextFailsBeforeProviderWhenMandatoryContentExceedsBudget(t *testing.T) {
	estimator := &fixedMessageEstimator{}
	providerCalled := false
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		providerCalled = true
		return nil, nil
	})
	runtime := NewRuntime(provider, tools.NewRegistry(), "fake-model", Options{Context: ContextOptions{
		ContextWindow:      15,
		OutputTokenReserve: 10,
		Estimator:          estimator,
	}})

	events := collectStreamEvents(t, runtime.Run(context.Background(), NewRunRequest([]Message{UserMessage{Content: "mandatory"}})))
	if providerCalled {
		t.Fatal("provider called despite mandatory context overflow")
	}
	failed, ok := events[len(events)-1].(RunFailedEvent)
	if !ok {
		t.Fatalf("last event = %T, want RunFailedEvent", events[len(events)-1])
	}
	var contextErr *ContextError
	if !errors.As(failed.Error, &contextErr) || contextErr.Code != ContextBudgetExceeded {
		t.Fatalf("failed error = %v, want context budget exceeded", failed.Error)
	}
}

func TestRuntimeContextMapsCompactorFailureToFailedTerminal(t *testing.T) {
	estimator := &fixedMessageEstimator{}
	compactor := compactorFunc(func(ctx context.Context, input ContextCompactionInput) (ContextSummary, error) {
		return ContextSummary{}, errors.New("summary provider unavailable")
	})
	providerCalled := false
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		providerCalled = true
		return nil, nil
	})
	runtime := NewRuntime(provider, tools.NewRegistry(), "fake-model", Options{Context: ContextOptions{
		ContextWindow: 25,
		Estimator:     estimator,
		Compactor:     compactor,
	}})
	request := NewRunRequest([]Message{
		UserMessage{Content: "old question"},
		AssistantMessage{Content: "old answer"},
		UserMessage{Content: "current question"},
	})

	events := collectStreamEvents(t, runtime.Run(context.Background(), request))
	if providerCalled {
		t.Fatal("provider called after compaction failure")
	}
	failed, ok := events[len(events)-1].(RunFailedEvent)
	if !ok {
		t.Fatalf("last event = %T, want RunFailedEvent", events[len(events)-1])
	}
	var contextErr *ContextError
	if !errors.As(failed.Error, &contextErr) || contextErr.Code != ContextCompactionFailed {
		t.Fatalf("failed error = %v, want context compaction failed", failed.Error)
	}
}

func TestRuntimeContextKeepsToolCallResultGroupAtomicWhenPruning(t *testing.T) {
	estimator := &fixedMessageEstimator{}
	recorder := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done"})), nil
	})}
	runtime := NewRuntime(recorder, tools.NewRegistry(), "fake-model", Options{Context: ContextOptions{
		ContextWindow: 30,
		Estimator:     estimator,
	}})
	oldCall := calculatorToolCall("old-call", `{"a":1,"b":2,"op":"add"}`)
	request := NewRunRequest([]Message{
		UserMessage{Content: "old tool question"},
		AssistantMessage{ToolCalls: []llms.ToolCall{oldCall}},
		ToolResultMessage{ToolCallID: oldCall.ID, ToolName: "calculator", Content: "3"},
		UserMessage{Content: "recent question"},
		AssistantMessage{Content: "recent answer"},
		UserMessage{Content: "current question"},
	})

	events := collectStreamEvents(t, runtime.Run(context.Background(), request))
	if _, ok := events[len(events)-1].(RunCompletedEvent); !ok {
		t.Fatalf("last event = %T, want RunCompletedEvent", events[len(events)-1])
	}
	got := recorder.requests[0].Messages
	if len(got) != 3 {
		t.Fatalf("provider messages = %#v, want complete recent turn and current input", got)
	}
	for _, message := range got {
		if message.ToolCallID == oldCall.ID || len(message.ToolCalls) != 0 {
			t.Fatalf("provider messages retained a partial old tool group: %#v", got)
		}
	}
}

func TestRuntimeContextIsPreparedBeforeEveryProviderRound(t *testing.T) {
	estimator := &fixedMessageEstimator{}
	provider, err := llms.NewFakeProvider(llms.ProviderConfig{Model: "fake-tool-model"})
	if err != nil {
		t.Fatalf("NewFakeProvider() error = %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})
	runtime := NewRuntime(provider, registry, "fake-tool-model", Options{Context: ContextOptions{
		ContextWindow: 1000,
		Estimator:     estimator,
	}})

	request := NewRunRequestWithOptions([]Message{
		UserMessage{Content: "use calculator to compute 13 * 7"},
	}, RunRequestOptions{AllowedTools: []AllowedTool{allowedCalculatorForTest(registry)}})
	events := collectStreamEvents(t, runtime.Run(context.Background(), request))
	preparedEvents := 0
	for _, event := range events {
		if _, ok := event.(ContextPreparedEvent); ok {
			preparedEvents++
		}
	}
	if preparedEvents != 2 {
		t.Fatalf("ContextPreparedEvent count = %d, want one before each of two provider rounds", preparedEvents)
	}
	if estimator.calls < preparedEvents {
		t.Fatalf("estimator calls = %d, want at least %d", estimator.calls, preparedEvents)
	}
}

func TestRuntimeContextMarksDefaultEstimatorApproximate(t *testing.T) {
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done"})), nil
	})
	runtime := NewRuntime(provider, tools.NewRegistry(), "fake-model", Options{Context: ContextOptions{
		ContextWindow:      1000,
		OutputTokenReserve: 100,
		SafetyMargin:       50,
	}})

	events := collectStreamEvents(t, runtime.Run(context.Background(), NewRunRequest([]Message{UserMessage{Content: "answer"}})))
	metadata := findContextPreparedEvent(t, events)
	if !metadata.Approximate || metadata.Estimator != "approximate-byte-upper-bound" {
		t.Fatalf("context metadata = %#v, want named approximate estimator", metadata)
	}
	if metadata.InputBudget != 850 || metadata.OutputTokenReserve != 100 || metadata.SafetyMargin != 50 {
		t.Fatalf("context metadata = %#v, want complete budget arithmetic", metadata)
	}
}

func TestRuntimeContextEstimatorCannotMutateProviderToolSchemas(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})
	estimator := tokenEstimatorFunc(func(ctx context.Context, input ContextEstimateInput) (TokenEstimate, error) {
		if len(input.Tools) != 1 {
			t.Fatalf("estimator tools = %d, want calculator schema", len(input.Tools))
		}
		parameters := input.Tools[0].Function.Parameters
		properties := parameters["properties"].(map[string]any)
		delete(properties, "a")
		required := parameters["required"].([]any)
		required[0] = "mutated"
		return TokenEstimate{Tokens: 1, Estimator: "mutating-test"}, nil
	})
	recorder := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done"})), nil
	})}
	runtime := NewRuntime(recorder, registry, "fake-model", Options{Context: ContextOptions{
		ContextWindow: 1000,
		Estimator:     estimator,
	}})

	request := NewRunRequestWithOptions([]Message{UserMessage{Content: "answer"}}, RunRequestOptions{AllowedTools: []AllowedTool{allowedCalculatorForTest(registry)}})
	events := collectStreamEvents(t, runtime.Run(context.Background(), request))
	if _, ok := events[len(events)-1].(RunCompletedEvent); !ok {
		t.Fatalf("last event = %T, want RunCompletedEvent", events[len(events)-1])
	}
	parameters := recorder.requests[0].Tools[0].Function.Parameters
	properties := parameters["properties"].(map[string]any)
	if _, ok := properties["a"]; !ok {
		t.Fatalf("provider schema = %#v, estimator deleted property a", parameters)
	}
	required := parameters["required"].([]any)
	if required[0] == "mutated" {
		t.Fatalf("provider schema = %#v, estimator mutated required slice", parameters)
	}
}

func TestRuntimeContextBudgetArithmeticSaturatesAtZero(t *testing.T) {
	providerCalled := false
	provider := streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		providerCalled = true
		return streamEvents(assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done"})), nil
	})
	maxInt := int(^uint(0) >> 1)
	runtime := NewRuntime(provider, tools.NewRegistry(), "fake-model", Options{Context: ContextOptions{
		ContextWindow:      100,
		OutputTokenReserve: maxInt,
		SafetyMargin:       maxInt,
		Estimator:          &fixedMessageEstimator{},
	}})

	events := collectStreamEvents(t, runtime.Run(context.Background(), NewRunRequest([]Message{UserMessage{Content: "mandatory"}})))
	if providerCalled {
		t.Fatal("provider called after overflowing reserve and safety margin")
	}
	failed, ok := events[len(events)-1].(RunFailedEvent)
	if !ok {
		t.Fatalf("last event = %T, want RunFailedEvent", events[len(events)-1])
	}
	var contextErr *ContextError
	if !errors.As(failed.Error, &contextErr) || contextErr.Code != ContextBudgetExceeded {
		t.Fatalf("failed error = %v, want context budget exceeded", failed.Error)
	}
}

func findContextPreparedEvent(t *testing.T, events []Event) ContextPreparedEvent {
	t.Helper()
	for _, event := range events {
		if metadata, ok := event.(ContextPreparedEvent); ok {
			return metadata
		}
	}
	t.Fatalf("events = %#v, want ContextPreparedEvent", events)
	return ContextPreparedEvent{}
}

func allowedCalculatorForTest(registry *tools.Registry) AllowedTool {
	schema := registry.Schemas()[0]
	return AllowedTool{
		Name:        schema.Function.Name,
		Version:     "test-v1",
		Description: schema.Function.Description,
		Parameters:  schema.Function.Parameters,
		Policy:      ToolPolicy{Concurrency: ToolConcurrencyExclusive},
		Executor: ToolExecutorFunc(func(ctx context.Context, request ToolExecutionRequest) (ToolExecutionResult, error) {
			content, err := registry.Call(ctx, request.ToolName, request.Arguments)
			return ToolExecutionResult{ModelContent: content}, err
		}),
	}
}
