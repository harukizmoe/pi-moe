# FixAgentRobustness Report

## Status
Implemented agent/provider/tool robustness fixes for review improvements #7, #8, #10, #11, and #12.

## Changes
- Normalized a nil agent tools registry to `tools.NewRegistry()` during construction.
- Preserved cancellation terminal `ErrorEvent` when the stream buffer is full by replacing a buffered event instead of silently dropping the terminal event.
- Rejected provider `done` messages whose role is neither empty nor `assistant`.
- Rejected invalid assistant tool-call argument JSON at the agent boundary before tool execution, which prevents malformed SSE `tool_call` arguments from being emitted.
- Replaced model/persisted tool error content with a safe summary while keeping the wrapped internal error on `ToolExecutionEndEvent.Error` for logs/events.

## RED evidence
- `go test ./internal/agent -run 'Test(NewWithOptionsTreatsNilToolsRegistryAsEmpty|EmitCancellationKeepsTerminalEventWhenStreamIsFull|AgentStreamRejectsProviderDoneWithNonAssistantRole|AgentStreamRejectsMalformedToolCallArgumentsBeforeExecution|ToolErrorContentIsSafeWhileEventKeepsInternalError)' -count=1` failed with the existing nil registry goroutine panic.
- `go test ./internal/agent -run 'Test(EmitCancellationKeepsTerminalEventWhenStreamIsFull|AgentStreamRejectsProviderDoneWithNonAssistantRole|AgentStreamRejectsMalformedToolCallArgumentsBeforeExecution|ToolErrorContentIsSafeWhileEventKeepsInternalError)' -count=1` failed for dropped cancellation event, accepted non-assistant done role, malformed tool args reaching execution, and leaked tool error content.

## GREEN evidence
- `go test ./internal/agent -run 'Test(NewWithOptionsTreatsNilToolsRegistryAsEmpty|EmitCancellationKeepsTerminalEventWhenStreamIsFull|AgentStreamRejectsProviderDoneWithNonAssistantRole|AgentStreamRejectsMalformedToolCallArgumentsBeforeExecution|ToolErrorContentIsSafeWhileEventKeepsInternalError)' -count=1` passed.
- `go test ./internal/agent -count=1` passed.

## Files changed
- `internal/agent/agent.go`
- `internal/agent/loop.go`
- `internal/agent/message.go`
- `internal/agent/tools.go`
- `internal/agent/loop_test.go`
- `internal/agent/streaming_provider_test.go`
- `internal/agent/robustness_test.go`

## Risks
- #10 is handled by rejecting malformed tool-call arguments at the agent boundary; `internal/application/service/session.go` was not touched because another agent owns active edits there.
- #15 duplicate max-steps cleanup remains intentionally untouched for `FixCleanupMinimal`.
