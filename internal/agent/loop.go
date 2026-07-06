package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"harukizmoe/pimoe/internal/llms"
)

var nextRunSequence atomic.Uint64

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

func (a *Agent) stream(ctx context.Context, messages []Message, stream chan<- Event) {
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
		llmMessages, err := toLLMMessages(messages)
		if err != nil {
			emit(ErrorEvent{RunID: runID, Error: err})
			return
		}
		a.logLLMRequest(ctx, chatRound, len(llmMessages), len(toolSchemas))
		response, err := a.provider.Chat(ctx, llms.ChatRequest{
			Model:    a.model,
			Messages: llmMessages,
			Tools:    toolSchemas,
		})
		if err != nil {
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

		assistantMessage := AssistantMessage{
			Content:   response.Message.Content,
			ToolCalls: append([]llms.ToolCall(nil), response.Message.ToolCalls...),
		}
		if _, err := toLLMMessage(assistantMessage); err != nil {
			emit(ErrorEvent{RunID: runID, Error: fmt.Errorf("assistant response: %w", err)})
			return
		}

		if len(assistantMessage.ToolCalls) > 0 && chatRound >= a.maxSteps {
			a.logger.Error(ctx, "agent.max_steps.exceeded", "max_steps", a.maxSteps, "tool_calls", len(assistantMessage.ToolCalls))
			emit(ErrorEvent{RunID: runID, Error: fmt.Errorf("agent max steps exceeded after %d tool-calling rounds", a.maxSteps)})
			return
		}

		messageID := fmt.Sprintf("%s-assistant-%d", runID, chatRound+1)
		if !emitAssistantLifecycle(emit, runID, messageID, assistantMessage) {
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

func emitAssistantLifecycle(emit func(Event) bool, runID string, messageID string, message AssistantMessage) bool {
	if !emit(MessageStartEvent{RunID: runID, MessageID: messageID, Role: "assistant"}) {
		return false
	}
	if strings.TrimSpace(message.Content) != "" {
		if !emit(MessageDeltaEvent{RunID: runID, MessageID: messageID, Kind: MessageDeltaText, ContentIndex: 0, Delta: message.Content}) {
			return false
		}
	}
	for i, call := range message.ToolCalls {
		if strings.TrimSpace(call.Function.Arguments) == "" {
			continue
		}
		if !emit(MessageDeltaEvent{RunID: runID, MessageID: messageID, Kind: MessageDeltaToolCall, ContentIndex: i, Delta: call.Function.Arguments}) {
			return false
		}
	}
	return emit(MessageEndEvent{RunID: runID, MessageID: messageID, Message: cloneAssistantMessage(message)})
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

func emitCancellation(stream chan<- Event, event Event) {
	select {
	case stream <- event:
	default:
	}
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
