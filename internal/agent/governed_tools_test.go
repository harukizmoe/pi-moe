package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

func TestRuntimeGovernedToolsPairDeniedUnknownAndInvalidCalls(t *testing.T) {
	var executions atomic.Int32
	allowed := governedTestTool("lookup", "lookup-v3", ToolPolicy{}, ToolExecutorFunc(func(context.Context, ToolExecutionRequest) (ToolExecutionResult, error) {
		executions.Add(1)
		return ToolExecutionResult{ModelContent: "unexpected"}, nil
	}))
	calls := []llms.ToolCall{
		governedToolCall("call_denied", "admin", `{"value":"x"}`),
		governedToolCall("call_unknown", "mystery", `{"value":"x"}`),
		governedToolCall("call_invalid", "lookup", `{"value":7}`),
	}
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		switch len(req.Messages) {
		case 1:
			if len(req.Tools) != 1 || req.Tools[0].Function.Name != "lookup" {
				t.Fatalf("provider tools = %#v, want only request-scoped lookup", req.Tools)
			}
			return streamEvents(
				assistantToolCallDelta(0, calls[0]),
				assistantToolCallDelta(1, calls[1]),
				assistantToolCallDelta(2, calls[2]),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: calls}),
			), nil
		case 5:
			wantContents := []string{`tool "admin" was denied`, `tool "mystery" is unavailable`, `tool "lookup" received invalid arguments`}
			for i, want := range wantContents {
				message := req.Messages[i+2]
				if message.Role != llms.RoleTool || message.ToolCallID != calls[i].ID || message.Content != want {
					t.Fatalf("paired result %d = %#v, want content %q", i, message, want)
				}
			}
			return streamEvents(assistantTextDelta("handled"), assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "handled"})), nil
		default:
			t.Fatalf("provider messages len = %d", len(req.Messages))
			return nil, nil
		}
	})}
	registry := tools.NewRegistry()
	registry.Register(tools.Calculator{})
	runtime := NewRuntime(provider, registry, "governed-model", Options{})
	request := NewRunRequestWithOptions(
		[]Message{UserMessage{Content: "try governed tools"}},
		RunRequestOptions{AllowedTools: []AllowedTool{allowed}, KnownToolNames: []string{"admin"}},
	)

	events := collectStreamEvents(t, runtime.Run(context.Background(), request))

	if executions.Load() != 0 {
		t.Fatalf("executor calls = %d, want none", executions.Load())
	}
	var starts []ToolExecutionStartEvent
	var ends []ToolExecutionEndEvent
	for _, event := range events {
		switch event := event.(type) {
		case ToolExecutionStartEvent:
			starts = append(starts, event)
		case ToolExecutionEndEvent:
			ends = append(ends, event)
		}
	}
	if len(starts) != 3 || len(ends) != 3 {
		t.Fatalf("tool lifecycle counts = starts %d ends %d", len(starts), len(ends))
	}
	wantStatuses := []ToolResultStatus{ToolResultDenied, ToolResultSkipped, ToolResultError}
	for i, want := range wantStatuses {
		if starts[i].ArgumentsDigest == "" {
			t.Fatalf("start %d leaked arguments or missed digest: %#v", i, starts[i])
		}
		if ends[i].Status != want || ends[i].Result.Status != want || ends[i].ArgumentsDigest == "" || ends[i].OutputDigest == "" {
			t.Fatalf("end %d = %#v, want status %q and audit digests", i, ends[i], want)
		}
	}
	if _, ok := events[len(events)-1].(RunCompletedEvent); !ok {
		t.Fatalf("last event = %T, want RunCompletedEvent", events[len(events)-1])
	}
}

func TestRuntimeGovernedToolsUseVersionedSchemaAndRunSerially(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	var executions atomic.Int32
	executor := ToolExecutorFunc(func(ctx context.Context, request ToolExecutionRequest) (ToolExecutionResult, error) {
		executions.Add(1)
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		return ToolExecutionResult{ModelContent: request.ToolCallID, InternalDetails: "internal-success-detail"}, nil
	})
	allowed := governedTestTool("lookup", "2026-07-12", ToolPolicy{Concurrency: ToolConcurrencyExclusive}, executor)
	calls := []llms.ToolCall{
		governedToolCall("call_1", "lookup", `{"value":"one"}`),
		governedToolCall("call_2", "lookup", `{"value":"two"}`),
	}
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		switch len(req.Messages) {
		case 1:
			parameters := req.Tools[0].Function.Parameters
			properties := parameters["properties"].(map[string]any)
			if _, ok := properties["value"]; !ok {
				t.Fatalf("request schema mutated after request construction: %#v", parameters)
			}
			return streamEvents(
				assistantToolCallDelta(0, calls[0]),
				assistantToolCallDelta(1, calls[1]),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: calls}),
			), nil
		case 4:
			return streamEvents(assistantTextDelta("done"), assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "done"})), nil
		default:
			t.Fatalf("provider messages len = %d", len(req.Messages))
			return nil, nil
		}
	})}
	runtime := NewRuntime(provider, tools.NewRegistry(), "governed-model", Options{})
	options := RunRequestOptions{AllowedTools: []AllowedTool{allowed}}
	request := NewRunRequestWithOptions([]Message{UserMessage{Content: "two lookups"}}, options)
	delete(options.AllowedTools[0].Parameters["properties"].(map[string]any), "value")

	events := collectStreamEvents(t, runtime.Run(context.Background(), request))

	if executions.Load() != 2 || maximum.Load() != 1 {
		t.Fatalf("executor calls/max concurrency = %d/%d, want 2/1", executions.Load(), maximum.Load())
	}
	var ends []ToolExecutionEndEvent
	for _, event := range events {
		if event, ok := event.(ToolExecutionEndEvent); ok {
			ends = append(ends, event)
		}
	}
	if len(ends) != 2 {
		t.Fatalf("tool end count = %d, want 2", len(ends))
	}
	for _, event := range ends {
		if event.Status != ToolResultSuccess || event.ToolVersion != "2026-07-12" || event.InternalDigest == digestString("") {
			t.Fatalf("tool end = %#v, want versioned success audit", event)
		}
	}
}

func TestRuntimeGovernedToolApprovalDeniedAndMissingGate(t *testing.T) {
	tests := []struct {
		name         string
		gate         ApprovalGate
		wantDecision string
	}{
		{name: "denied", gate: ApprovalGateFunc(func(context.Context, ApprovalRequest) (ApprovalDecision, error) {
			return ApprovalDecision{Approved: false, Reason: "contains secret text"}, nil
		}), wantDecision: "denied"},
		{name: "missing gate", gate: nil, wantDecision: "missing_gate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var executions atomic.Int32
			allowed := governedTestTool("write", "write-v1", ToolPolicy{RequiresApproval: true}, ToolExecutorFunc(func(context.Context, ToolExecutionRequest) (ToolExecutionResult, error) {
				executions.Add(1)
				return ToolExecutionResult{ModelContent: "unexpected"}, nil
			}))
			call := governedToolCall("call_write", "write", `{"value":"sensitive payload"}`)
			provider := &recordingProvider{inner: governedSingleCallProvider(t, call)}
			runtime := NewRuntime(provider, tools.NewRegistry(), "governed-model", Options{})
			request := NewRunRequestWithOptions([]Message{UserMessage{Content: "write"}}, RunRequestOptions{AllowedTools: []AllowedTool{allowed}, ApprovalGate: tt.gate})

			events := collectStreamEvents(t, runtime.Run(context.Background(), request))

			if executions.Load() != 0 {
				t.Fatalf("executor calls = %d, want 0", executions.Load())
			}
			requestedIndex, decidedIndex, endIndex := -1, -1, -1
			var decision ToolApprovalDecidedEvent
			for i, event := range events {
				switch event := event.(type) {
				case ToolApprovalRequestedEvent:
					requestedIndex = i
					if event.ArgumentsDigest == "" {
						t.Fatal("approval request missing arguments digest")
					}
				case ToolApprovalDecidedEvent:
					decidedIndex = i
					decision = event
				case ToolExecutionEndEvent:
					endIndex = i
					if event.Status != ToolResultDenied || event.Result.Status != ToolResultDenied {
						t.Fatalf("tool end = %#v, want denied", event)
					}
				}
			}
			if requestedIndex < 0 || decidedIndex <= requestedIndex || endIndex <= decidedIndex {
				t.Fatalf("approval event order = requested %d decided %d end %d", requestedIndex, decidedIndex, endIndex)
			}
			if decision.Approved || decision.Decision != tt.wantDecision {
				t.Fatalf("decision = %#v, want safe code %q", decision, tt.wantDecision)
			}
		})
	}
}

func TestRuntimeGovernedToolTimeoutReturnsSafePairedResult(t *testing.T) {
	allowed := governedTestTool("slow", "slow-v1", ToolPolicy{Timeout: 5 * time.Millisecond}, ToolExecutorFunc(func(ctx context.Context, request ToolExecutionRequest) (ToolExecutionResult, error) {
		<-ctx.Done()
		return ToolExecutionResult{InternalDetails: "database password=secret"}, ctx.Err()
	}))
	call := governedToolCall("call_slow", "slow", `{"value":"secret argument"}`)
	provider := &recordingProvider{inner: governedSingleCallProvider(t, call)}
	runtime := NewRuntime(provider, tools.NewRegistry(), "governed-model", Options{})
	request := NewRunRequestWithOptions([]Message{UserMessage{Content: "slow call"}}, RunRequestOptions{AllowedTools: []AllowedTool{allowed}})

	events := collectStreamEvents(t, runtime.Run(context.Background(), request))

	var toolEnd ToolExecutionEndEvent
	for _, event := range events {
		if event, ok := event.(ToolExecutionEndEvent); ok {
			toolEnd = event
		}
	}
	if toolEnd.Status != ToolResultTimeout || toolEnd.Result.Content != `tool "slow" timed out` || toolEnd.Result.Status != ToolResultTimeout {
		t.Fatalf("tool end = %#v, want timeout result", toolEnd)
	}
	if toolEnd.InternalDigest == digestString("") || toolEnd.Error == nil || !errors.Is(toolEnd.Error, context.DeadlineExceeded) {
		t.Fatalf("timeout classification/digest = %#v", toolEnd)
	}
}

func TestRuntimeCancellationPairsRemainingBatchCalls(t *testing.T) {
	entered := make(chan struct{})
	var executions atomic.Int32
	allowed := governedTestTool("wait", "wait-v1", ToolPolicy{}, ToolExecutorFunc(func(ctx context.Context, request ToolExecutionRequest) (ToolExecutionResult, error) {
		if executions.Add(1) == 1 {
			close(entered)
		}
		<-ctx.Done()
		return ToolExecutionResult{InternalDetails: "canceled internal"}, ctx.Err()
	}))
	calls := []llms.ToolCall{
		governedToolCall("call_wait_1", "wait", `{"value":"one"}`),
		governedToolCall("call_wait_2", "wait", `{"value":"two"}`),
	}
	provider := &recordingProvider{inner: streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		return streamEvents(
			assistantToolCallDelta(0, calls[0]),
			assistantToolCallDelta(1, calls[1]),
			assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: calls}),
		), nil
	})}
	runtime := NewRuntime(provider, tools.NewRegistry(), "governed-model", Options{})
	request := NewRunRequestWithOptions([]Message{UserMessage{Content: "wait twice"}}, RunRequestOptions{AllowedTools: []AllowedTool{allowed}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-entered
		cancel()
	}()

	events := collectStreamEvents(t, runtime.Run(ctx, request))

	if executions.Load() != 1 {
		t.Fatalf("executor calls = %d, want only first call", executions.Load())
	}
	var statuses []ToolResultStatus
	for _, event := range events {
		if event, ok := event.(ToolExecutionEndEvent); ok {
			statuses = append(statuses, event.Status)
		}
	}
	if len(statuses) != 2 || statuses[0] != ToolResultCanceled || statuses[1] != ToolResultSkipped {
		t.Fatalf("paired statuses = %#v, want canceled then skipped", statuses)
	}
	if _, ok := events[len(events)-1].(RunCanceledEvent); !ok {
		t.Fatalf("last event = %T, want RunCanceledEvent", events[len(events)-1])
	}
}

func governedTestTool(name string, version string, policy ToolPolicy, executor ToolExecutor) AllowedTool {
	return AllowedTool{
		Name:        name,
		Version:     version,
		Description: "test governed tool",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
			"required":             []string{"value"},
			"additionalProperties": false,
		},
		Policy:   policy,
		Executor: executor,
	}
}

func governedToolCall(id string, name string, arguments string) llms.ToolCall {
	return llms.ToolCall{ID: id, Type: "function", Function: llms.ToolCallFunction{Name: name, Arguments: arguments}}
}

func governedSingleCallProvider(t *testing.T, call llms.ToolCall) llms.Provider {
	t.Helper()
	return streamFunc(func(ctx context.Context, req llms.ChatRequest) (<-chan llms.ChatStreamEvent, error) {
		switch len(req.Messages) {
		case 1:
			return streamEvents(
				assistantToolCallDelta(0, call),
				assistantDone(llms.Message{Role: llms.RoleAssistant, ToolCalls: []llms.ToolCall{call}}),
			), nil
		case 3:
			if result := req.Messages[2]; result.Role != llms.RoleTool || result.ToolCallID != call.ID {
				t.Fatalf("paired tool result = %#v", result)
			}
			return streamEvents(assistantTextDelta("handled"), assistantDone(llms.Message{Role: llms.RoleAssistant, Content: "handled"})), nil
		default:
			t.Fatalf("provider messages len = %d", len(req.Messages))
			return nil, nil
		}
	})
}

func TestRuntimeGovernedToolApprovalAllowsExecution(t *testing.T) {
	var executed atomic.Int32
	allowed := governedTestTool("write", "write-v1", ToolPolicy{RequiresApproval: true}, ToolExecutorFunc(func(context.Context, ToolExecutionRequest) (ToolExecutionResult, error) {
		executed.Add(1)
		return ToolExecutionResult{ModelContent: "written"}, nil
	}))
	call := governedToolCall("call_write", "write", `{"value":"safe"}`)
	gate := ApprovalGateFunc(func(ctx context.Context, request ApprovalRequest) (ApprovalDecision, error) {
		if request.ToolVersion != "write-v1" || request.ArgumentsDigest == "" {
			t.Fatalf("approval request = %#v", request)
		}
		return ApprovalDecision{Approved: true, Reason: "approved"}, nil
	})
	provider := &recordingProvider{inner: governedSingleCallProvider(t, call)}
	runtime := NewRuntime(provider, tools.NewRegistry(), "governed-model", Options{})
	request := NewRunRequestWithOptions([]Message{UserMessage{Content: "write"}}, RunRequestOptions{AllowedTools: []AllowedTool{allowed}, ApprovalGate: gate})
	events := collectStreamEvents(t, runtime.Run(context.Background(), request))

	if executed.Load() != 1 {
		t.Fatalf("executor calls = %d, want 1", executed.Load())
	}
	var decided ToolApprovalDecidedEvent
	var end ToolExecutionEndEvent
	for _, event := range events {
		switch event := event.(type) {
		case ToolApprovalDecidedEvent:
			decided = event
		case ToolExecutionEndEvent:
			end = event
		}
	}
	if !decided.Approved || decided.Decision != "approved" || end.Status != ToolResultSuccess {
		t.Fatalf("approval decision/end = %#v / %#v", decided, end)
	}
}
