package agent

import (
	"context"
	"fmt"
	"strings"

	"harukizmoe/pimoe/internal/llms"
)

// Run 执行一次有步数上限的 tool calling 主循环，并返回最终回答。
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	result, err := a.RunResult(ctx, input)
	if err != nil {
		return "", err
	}
	return result.Answer, nil
}

// RunResult 执行一次有步数上限的 tool calling 主循环，并返回结构化 trace。
func (a *Agent) RunResult(ctx context.Context, input string) (*RunResult, error) {
	return a.RunMessages(ctx, []llms.Message{{Role: llms.RoleUser, Content: input}})
}

// RunMessages 从调用方提供的无状态对话历史继续执行 tool calling 主循环。
func (a *Agent) RunMessages(ctx context.Context, messages []llms.Message) (*RunResult, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}
	lastMessage := messages[len(messages)-1]
	if lastMessage.Role != llms.RoleUser || strings.TrimSpace(lastMessage.Content) == "" {
		return nil, fmt.Errorf("last message must be a non-empty user message")
	}

	result := &RunResult{}
	messages = append([]llms.Message(nil), messages...)
	toolSchemas := a.tools.Schemas()

	a.logger.Info(ctx, "agent.run.start", "model", a.model, "input", lastMessage.Content)
	for chatRound := 0; ; chatRound++ {
		a.logLLMRequest(ctx, chatRound, len(messages), len(toolSchemas))
		response, err := a.provider.Chat(ctx, llms.ChatRequest{
			Model:    a.model,
			Messages: messages,
			Tools:    toolSchemas,
		})
		if err != nil {
			a.logLLMError(ctx, chatRound, err)
			return result, fmt.Errorf("llm chat round %d: %w", chatRound+1, err)
		}

		assistantMessage := response.Message
		if len(assistantMessage.ToolCalls) == 0 {
			result.Answer = assistantMessage.Content
			a.logger.Info(ctx, "agent.run.done", "answer", assistantMessage.Content)
			a.emit(Event{Type: EventFinal, Message: assistantMessage.Content})
			return result, nil
		}

		if result.ToolRounds >= a.maxSteps {
			a.logger.Error(ctx, "agent.max_steps.exceeded", "max_steps", a.maxSteps, "tool_calls", len(assistantMessage.ToolCalls))
			return result, fmt.Errorf("agent max steps exceeded after %d tool-calling rounds", a.maxSteps)
		}

		a.logger.Info(ctx, "agent.tool_calls.received", "count", len(assistantMessage.ToolCalls))
		messages = append(messages, assistantMessage)
		for _, call := range assistantMessage.ToolCalls {
			a.logger.Debug(ctx, "agent.tool.call", "name", call.Function.Name, "arguments", call.Function.Arguments)
			a.emit(Event{Type: EventToolCall, Message: call.Function.Name})

			step := Step{ToolCallID: call.ID, ToolName: call.Function.Name, Arguments: call.Function.Arguments}
			toolMessage, err := a.runToolCall(ctx, call)
			if err != nil {
				step.Error = err.Error()
				result.Steps = append(result.Steps, step)
				a.logger.Error(ctx, "agent.tool.error", "name", call.Function.Name, "error", err)
				return result, err
			}

			step.Result = toolMessage.Content
			result.Steps = append(result.Steps, step)
			a.logger.Debug(ctx, "agent.tool.result", "name", call.Function.Name, "content", toolMessage.Content)
			a.emit(Event{Type: EventToolResult, Message: toolMessage.Content})
			messages = append(messages, toolMessage)
		}
		result.ToolRounds++
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
