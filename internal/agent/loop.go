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
	return a.RunAgentMessages(ctx, []Message{UserMessage{Content: input}})
}

// RunMessages 从调用方提供的无状态 LLM DTO 历史继续执行 tool calling 主循环。
func (a *Agent) RunMessages(ctx context.Context, messages []llms.Message) (*RunResult, error) {
	agentMessages, err := fromLLMMessages(messages)
	if err != nil {
		return nil, err
	}
	return a.RunAgentMessages(ctx, agentMessages)
}

// RunAgentMessages 从调用方提供的强语义无状态对话历史继续执行 tool calling 主循环。
func (a *Agent) RunAgentMessages(ctx context.Context, messages []Message) (*RunResult, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}
	lastMessage, ok := messages[len(messages)-1].(UserMessage)
	if !ok || strings.TrimSpace(lastMessage.Content) == "" {
		return nil, fmt.Errorf("last message must be a non-empty user message")
	}

	messages = append([]Message(nil), messages...)
	if _, err := toLLMMessages(messages); err != nil {
		return nil, err
	}

	result := &RunResult{}
	toolSchemas := a.tools.Schemas()

	a.logger.Info(ctx, "agent.run.start", "model", a.model, "input", strings.TrimSpace(lastMessage.Content))
	for chatRound := 0; ; chatRound++ {
		llmMessages, err := toLLMMessages(messages)
		if err != nil {
			return result, err
		}
		a.logLLMRequest(ctx, chatRound, len(llmMessages), len(toolSchemas))
		response, err := a.provider.Chat(ctx, llms.ChatRequest{
			Model:    a.model,
			Messages: llmMessages,
			Tools:    toolSchemas,
		})
		if err != nil {
			a.logLLMError(ctx, chatRound, err)
			return result, fmt.Errorf("llm chat round %d: %w", chatRound+1, err)
		}

		assistantMessage := AssistantMessage{
			Content:   response.Message.Content,
			ToolCalls: append([]llms.ToolCall(nil), response.Message.ToolCalls...),
		}
		if _, err := toLLMMessage(assistantMessage); err != nil {
			return result, fmt.Errorf("assistant response: %w", err)
		}
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
			messages = append(messages, ToolResultMessage{ToolCallID: toolMessage.ToolCallID, ToolName: call.Function.Name, Content: toolMessage.Content})
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
