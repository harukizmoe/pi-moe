package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"harukizmoe/pimoe/internal/llms"
)

var nextRunSequence atomic.Uint64

type maxStepsExceededError struct {
	maxSteps  int
	toolCalls int
}

func (e maxStepsExceededError) Error() string {
	return fmt.Sprintf("agent max steps exceeded after %d tool-calling rounds", e.maxSteps)
}

// Stream 从调用方提供的强语义无状态对话历史继续执行 Agent，并通过 channel 返回运行事件。
func (a *Agent) Stream(ctx context.Context, messages []Message) <-chan Event {
	stream := make(chan Event, 64)
	go func() {
		defer close(stream)
		a.stream(ctx, messages, stream)
	}()
	return stream
}

func newRunID() string {
	return fmt.Sprintf("run-%d", nextRunSequence.Add(1))
}

func (a *Agent) stream(ctx context.Context, messages []Message, stream chan Event) {
	if ctx == nil {
		ctx = context.Background()
	}
	emit := func(event Event) bool {
		return emitEvent(ctx, stream, event)
	}

	if len(messages) == 0 {
		emit(ErrorEvent{Error: fmt.Errorf("messages must not be empty")})
		return
	}
	lastMessage, ok := messages[len(messages)-1].(UserMessage)
	if !ok || strings.TrimSpace(lastMessage.Content) == "" {
		emit(ErrorEvent{Error: fmt.Errorf("last message must be a non-empty user message")})
		return
	}

	messages = cloneMessages(messages)
	if _, err := toLLMMessages(messages); err != nil {
		emit(ErrorEvent{Error: err})
		return
	}
	if err := ctx.Err(); err != nil {
		emitCancellation(stream, ErrorEvent{Error: err})
		return
	}

	runID := newRunID()
	turn := countUserMessages(messages)
	toolSchemas := a.tools.Schemas()
	trimmedInput := strings.TrimSpace(lastMessage.Content)
	a.logger.Info(ctx, "agent.run.start", "model", a.model, "input", trimmedInput)
	if !emit(RunStartEvent{RunID: runID}) {
		return
	}
	if !emit(TurnStartEvent{RunID: runID, Turn: turn, UserMessage: UserMessage{Content: trimmedInput}}) {
		return
	}

	for chatRound := 0; ; chatRound++ {
		llmMessages, err := toLLMMessagesWithPrompts(messages, a.basePrompt, a.sessionPrompt)
		if err != nil {
			emit(ErrorEvent{RunID: runID, Error: err})
			return
		}
		a.logLLMRequest(ctx, chatRound, len(llmMessages), len(toolSchemas))
		messageID := fmt.Sprintf("%s-assistant-%d", runID, chatRound+1)
		assistantMessage, err := a.runChatRound(ctx, emit, runID, messageID, chatRound, llms.ChatRequest{
			Model:    a.model,
			Messages: llmMessages,
			Tools:    toolSchemas,
		})
		if err != nil {
			var maxStepsErr maxStepsExceededError
			if errors.As(err, &maxStepsErr) {
				a.logger.Error(ctx, "agent.max_steps.exceeded", "max_steps", maxStepsErr.maxSteps, "tool_calls", maxStepsErr.toolCalls)
				emit(ErrorEvent{RunID: runID, Error: maxStepsErr})
				return
			}
			a.logLLMError(ctx, chatRound, err)
			event := ErrorEvent{RunID: runID, Error: fmt.Errorf("llm chat round %d: %w", chatRound+1, err)}
			if ctx.Err() != nil {
				emitCancellation(stream, event)
				return
			}
			emit(event)
			return
		}
		if err := ctx.Err(); err != nil {
			emitCancellation(stream, ErrorEvent{RunID: runID, Error: err})
			return
		}

		if len(assistantMessage.ToolCalls) == 0 {
			messages = append(messages, assistantMessage)
			a.logger.Info(ctx, "agent.run.done", "answer", assistantMessage.Content)
			if !emit(TurnEndEvent{RunID: runID, Turn: turn}) {
				return
			}
			emit(RunEndEvent{RunID: runID})
			return
		}

		a.logger.Info(ctx, "agent.tool_calls.received", "count", len(assistantMessage.ToolCalls))
		messages = append(messages, assistantMessage)
		for _, call := range assistantMessage.ToolCalls {
			a.logger.Debug(ctx, "agent.tool.call", "name", call.Function.Name, "arguments", call.Function.Arguments)
			if !emit(ToolExecutionStartEvent{RunID: runID, ToolName: call.Function.Name, ToolCallID: call.ID, Arguments: call.Function.Arguments}) {
				return
			}

			toolMessage, err := a.runToolCall(ctx, call)
			if err != nil {
				a.logger.Error(ctx, "agent.tool.error", "name", call.Function.Name, "error", err)
			} else {
				a.logger.Debug(ctx, "agent.tool.result", "name", call.Function.Name, "content", toolMessage.Content)
			}
			if !emit(ToolExecutionEndEvent{RunID: runID, ToolCallID: call.ID, Result: toolMessage, Error: err}) {
				return
			}
			messages = append(messages, toolMessage)
		}
	}
}

func (a *Agent) runChatRound(ctx context.Context, emit func(Event) bool, runID string, messageID string, chatRound int, req llms.ChatRequest) (AssistantMessage, error) {
	return streamAssistantMessage(ctx, emit, a.provider, runID, messageID, chatRound, a.maxSteps, req)
}

func streamAssistantMessage(ctx context.Context, emit func(Event) bool, provider llms.Provider, runID string, messageID string, chatRound int, maxSteps int, req llms.ChatRequest) (AssistantMessage, error) {
	stream, err := provider.ChatStream(ctx, req)
	if err != nil {
		return AssistantMessage{}, err
	}
	if !emit(MessageStartEvent{RunID: runID, MessageID: messageID, Role: "assistant"}) {
		return AssistantMessage{}, ctx.Err()
	}

	for {
		select {
		case event, ok := <-stream:
			if !ok {
				return AssistantMessage{}, fmt.Errorf("llm chat stream closed without done")
			}
			switch event.Type {
			case llms.ChatStreamEventTypeDelta:
				if !emitChatStreamDelta(emit, runID, messageID, event.Delta) {
					return AssistantMessage{}, ctx.Err()
				}
			case llms.ChatStreamEventTypeDone:
				if err := validateProviderDoneRole(event.Message.Role); err != nil {
					return AssistantMessage{}, err
				}
				assistantMessage := assistantFromLLMMessage(event.Message)
				if err := validateAssistantMessage(assistantMessage); err != nil {
					return AssistantMessage{}, err
				}
				if len(assistantMessage.ToolCalls) > 0 && chatRound >= maxSteps {
					return AssistantMessage{}, maxStepsExceededError{maxSteps: maxSteps, toolCalls: len(assistantMessage.ToolCalls)}
				}
				if !emit(MessageEndEvent{RunID: runID, MessageID: messageID, Message: cloneAssistantMessage(assistantMessage)}) {
					return AssistantMessage{}, ctx.Err()
				}
				return assistantMessage, nil
			case llms.ChatStreamEventTypeError:
				if event.Err == nil {
					return AssistantMessage{}, fmt.Errorf("llm chat stream returned nil error")
				}
				return AssistantMessage{}, event.Err
			default:
				return AssistantMessage{}, fmt.Errorf("unknown llm chat stream event type %q", event.Type)
			}
		case <-ctx.Done():
			return AssistantMessage{}, ctx.Err()
		}
	}
}

func emitChatStreamDelta(emit func(Event) bool, runID string, messageID string, delta llms.ChatStreamDelta) bool {
	if delta.Content != "" {
		if !emit(MessageDeltaEvent{RunID: runID, MessageID: messageID, Kind: MessageDeltaText, ContentIndex: 0, Delta: delta.Content}) {
			return false
		}
	}
	if delta.ReasoningContent != "" {
		if !emit(MessageDeltaEvent{RunID: runID, MessageID: messageID, Kind: MessageDeltaThinking, ContentIndex: 0, Delta: delta.ReasoningContent}) {
			return false
		}
	}
	for _, call := range delta.ToolCalls {
		if call.Function.Arguments == "" {
			continue
		}
		if !emit(MessageDeltaEvent{RunID: runID, MessageID: messageID, Kind: MessageDeltaToolCall, ContentIndex: call.Index, Delta: call.Function.Arguments}) {
			return false
		}
	}
	return true
}

func assistantFromLLMMessage(message llms.Message) AssistantMessage {
	return AssistantMessage{
		Content:   message.Content,
		ToolCalls: append([]llms.ToolCall(nil), message.ToolCalls...),
	}
}

func validateProviderDoneRole(role llms.Role) error {
	if role == "" || role == llms.RoleAssistant {
		return nil
	}
	return fmt.Errorf("assistant response role must be empty or %q, got %q", llms.RoleAssistant, role)
}

func validateAssistantMessage(message AssistantMessage) error {
	if _, err := toLLMMessage(message); err != nil {
		return fmt.Errorf("assistant response: %w", err)
	}
	return nil
}

func countUserMessages(messages []Message) int {
	turn := 0
	for _, message := range messages {
		if _, ok := message.(UserMessage); ok {
			turn++
		}
	}
	if turn < 1 {
		return 1
	}
	return turn
}

func cloneMessages(messages []Message) []Message {
	out := make([]Message, len(messages))
	for i, message := range messages {
		switch msg := message.(type) {
		case UserMessage:
			out[i] = msg
		case AssistantMessage:
			out[i] = cloneAssistantMessage(msg)
		case ToolResultMessage:
			out[i] = msg
		default:
			out[i] = message
		}
	}
	return out
}

func cloneAssistantMessage(message AssistantMessage) AssistantMessage {
	return AssistantMessage{
		Content:   message.Content,
		ToolCalls: append([]llms.ToolCall(nil), message.ToolCalls...),
	}
}

func emitCancellation(stream chan Event, event Event) {
	select {
	case stream <- event:
		return
	default:
	}
	if cap(stream) == 0 {
		return
	}
	select {
	case <-stream:
	default:
	}
	stream <- event
}

func emitEvent(ctx context.Context, stream chan<- Event, event Event) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return false
	}
	select {
	case stream <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func (a *Agent) logLLMRequest(ctx context.Context, chatRound int, messages int, tools int) {
	if chatRound == 0 {
		a.logger.Debug(ctx, "agent.llm.first.request", "messages", messages, "tools", tools)
		return
	}
	a.logger.Debug(ctx, "agent.llm.final.request", "messages", messages)
}

func (a *Agent) logLLMError(ctx context.Context, chatRound int, err error) {
	if chatRound == 0 {
		a.logger.Error(ctx, "agent.llm.first.error", "error", err)
		return
	}
	a.logger.Error(ctx, "agent.llm.final.error", "error", err)
}
