package agent

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

// Run 执行一次有步数上限的 tool calling 主循环。
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	messages := []llms.Message{{Role: llms.RoleUser, Content: input}}
	toolSchemas := a.tools.Schemas()

	a.logger.Info(ctx, "agent.run.start", "model", a.model, "input", input)
	toolRounds := 0
	for chatRound := 0; ; chatRound++ {
		a.logLLMRequest(ctx, chatRound, len(messages), len(toolSchemas))
		response, err := a.provider.Chat(ctx, llms.ChatRequest{
			Model:    a.model,
			Messages: messages,
			Tools:    toolSchemas,
		})
		if err != nil {
			a.logLLMError(ctx, chatRound, err)
			return "", fmt.Errorf("llm chat round %d: %w", chatRound+1, err)
		}

		assistantMessage := response.Message
		if len(assistantMessage.ToolCalls) == 0 {
			a.logger.Info(ctx, "agent.run.done", "answer", assistantMessage.Content)
			a.emit(Event{Type: EventFinal, Message: assistantMessage.Content})
			return assistantMessage.Content, nil
		}

		if toolRounds >= a.maxSteps {
			a.logger.Error(ctx, "agent.max_steps.exceeded", "max_steps", a.maxSteps, "tool_calls", len(assistantMessage.ToolCalls))
			return "", fmt.Errorf("agent max steps exceeded after %d tool-calling rounds", a.maxSteps)
		}

		a.logger.Info(ctx, "agent.tool_calls.received", "count", len(assistantMessage.ToolCalls))
		messages = append(messages, assistantMessage)
		for _, call := range assistantMessage.ToolCalls {
			a.logger.Debug(ctx, "agent.tool.call", "name", call.Function.Name, "arguments", call.Function.Arguments)
			a.emit(Event{Type: EventToolCall, Message: call.Function.Name})

			toolMessage, err := a.runToolCall(ctx, call)
			if err != nil {
				a.logger.Error(ctx, "agent.tool.error", "name", call.Function.Name, "error", err)
				return "", err
			}

			a.logger.Debug(ctx, "agent.tool.result", "name", call.Function.Name, "content", toolMessage.Content)
			a.emit(Event{Type: EventToolResult, Message: toolMessage.Content})
			messages = append(messages, toolMessage)
		}
		toolRounds++
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

func (a *Agent) emit(event Event) {
	if a.onEvent != nil {
		a.onEvent(event)
	}
}
