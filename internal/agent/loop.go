package agent

import (
	"context"
	"fmt"
	"strings"

	"harukizmoe/pimoe/internal/llms"
)

// RunAgentMessages 从调用方提供的强语义无状态对话历史继续执行 tool calling 主循环。
func (a *Agent) RunAgentMessages(ctx context.Context, messages []Message) (*RunResult, error) {
	return collectRunResult(ctx, a.StreamAgentMessages(ctx, messages))
}

// StreamAgentMessages 从强语义无状态对话历史继续执行 Agent，并通过 channel 返回运行事件。
func (a *Agent) StreamAgentMessages(ctx context.Context, messages []Message) <-chan Event {
	stream := make(chan Event)
	go func() {
		defer close(stream)
		a.streamAgentMessages(ctx, messages, stream)
	}()
	return stream
}

func (a *Agent) streamAgentMessages(ctx context.Context, messages []Message, stream chan<- Event) {
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

	messages = append([]Message(nil), messages...)
	if _, err := toLLMMessages(messages); err != nil {
		emit(ErrorEvent{Error: err})
		return
	}

	toolSchemas := a.tools.Schemas()
	trimmedInput := strings.TrimSpace(lastMessage.Content)
	a.logger.Info(ctx, "agent.run.start", "model", a.model, "input", trimmedInput)
	if !emit(RunStartEvent{Input: trimmedInput}) {
		return
	}
	for chatRound := 0; ; chatRound++ {
		llmMessages, err := toLLMMessages(messages)
		if err != nil {
			emit(ErrorEvent{Error: err})
			return
		}
		round := chatRound + 1
		if !emit(LLMRequestEvent{Round: round}) {
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
			if !emit(LLMErrorEvent{Round: round, Error: err}) {
				return
			}
			emit(ErrorEvent{Error: fmt.Errorf("llm chat round %d: %w", round, err)})
			return
		}
		if ctx.Err() != nil {
			return
		}

		assistantMessage := AssistantMessage{
			Content:   response.Message.Content,
			ToolCalls: append([]llms.ToolCall(nil), response.Message.ToolCalls...),
		}
		if _, err := toLLMMessage(assistantMessage); err != nil {
			emit(ErrorEvent{Error: fmt.Errorf("assistant response: %w", err)})
			return
		}
		if len(assistantMessage.ToolCalls) == 0 {
			a.logger.Info(ctx, "agent.run.done", "answer", assistantMessage.Content)
			if !emit(FinalEvent{Answer: assistantMessage.Content}) {
				return
			}
			emit(RunEndEvent{Answer: assistantMessage.Content})
			return
		}

		if chatRound >= a.maxSteps {
			a.logger.Error(ctx, "agent.max_steps.exceeded", "max_steps", a.maxSteps, "tool_calls", len(assistantMessage.ToolCalls))
			emit(ErrorEvent{Error: fmt.Errorf("agent max steps exceeded after %d tool-calling rounds", a.maxSteps)})
			return
		}

		toolRound := chatRound + 1
		a.logger.Info(ctx, "agent.tool_calls.received", "count", len(assistantMessage.ToolCalls))
		messages = append(messages, assistantMessage)
		for _, call := range assistantMessage.ToolCalls {
			a.logger.Debug(ctx, "agent.tool.call", "name", call.Function.Name, "arguments", call.Function.Arguments)
			if !emit(ToolCallEvent{Round: toolRound, ToolName: call.Function.Name, ToolCallID: call.ID, Arguments: call.Function.Arguments}) {
				return
			}

			toolMessage, err := a.runToolCall(ctx, call)
			if err != nil {
				a.logger.Error(ctx, "agent.tool.error", "name", call.Function.Name, "error", err)
			} else {
				a.logger.Debug(ctx, "agent.tool.result", "name", call.Function.Name, "content", toolMessage.Content)
			}
			if !emit(ToolResultEvent{Round: toolRound, ToolName: call.Function.Name, ToolCallID: call.ID, Result: toolMessage.Content, Error: err}) {
				return
			}
			messages = append(messages, toolMessage)
		}
	}
}

func collectRunResult(ctx context.Context, stream <-chan Event) (*RunResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := &RunResult{}
	stepByCallID := make(map[string]int)
	var observedErr error
	var terminalErr error
	terminal := false

	for event := range stream {
		switch event := event.(type) {
		case ToolCallEvent:
			stepByCallID[event.ToolCallID] = len(result.Steps)
			result.Steps = append(result.Steps, Step{
				ToolCallID: event.ToolCallID,
				ToolName:   event.ToolName,
				Arguments:  event.Arguments,
			})
		case ToolResultEvent:
			if event.Round > result.ToolRounds {
				result.ToolRounds = event.Round
			}
			stepIndex, ok := stepByCallID[event.ToolCallID]
			if !ok {
				stepIndex = len(result.Steps)
				stepByCallID[event.ToolCallID] = stepIndex
				result.Steps = append(result.Steps, Step{ToolCallID: event.ToolCallID, ToolName: event.ToolName})
			}
			if event.Error != nil {
				result.Steps[stepIndex].Error = event.Error.Error()
			} else {
				result.Steps[stepIndex].Result = event.Result
			}
		case FinalEvent:
			result.Answer = event.Answer
		case RunEndEvent:
			terminal = true
		case LLMErrorEvent:
			if observedErr == nil {
				observedErr = event.Error
			}
		case ErrorEvent:
			terminal = true
			terminalErr = event.Error
		}
	}

	if terminalErr != nil {
		return result, terminalErr
	}
	if !terminal {
		if err := ctx.Err(); err != nil {
			return result, err
		}
	}
	if observedErr != nil {
		return result, observedErr
	}
	return result, nil
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
