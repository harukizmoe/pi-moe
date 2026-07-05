package agent

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

// Run 执行一次最多一轮的 tool calling：先请求模型，再执行工具，最后回填工具结果获取最终答案。
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	messages := []llms.Message{{Role: llms.RoleUser, Content: input}}
	toolSchemas := a.tools.Schemas()

	a.logger.Info(ctx, "agent.run.start", "model", a.model, "input", input)
	a.logger.Debug(ctx, "agent.llm.first.request", "messages", len(messages), "tools", len(toolSchemas))
	// 第一次对话把可用工具 schema 暴露给模型，让其决定是否发起 tool call。
	first, err := a.provider.Chat(ctx, llms.ChatRequest{
		Model:    a.model,
		Messages: messages,
		Tools:    toolSchemas,
	})
	if err != nil {
		a.logger.Error(ctx, "agent.llm.first.error", "error", err)
		return "", fmt.Errorf("first llm chat: %w", err)
	}

	assistantMessage := first.Message
	if len(assistantMessage.ToolCalls) == 0 {
		a.logger.Info(ctx, "agent.llm.first.final", "content", assistantMessage.Content)
		a.logger.Info(ctx, "agent.run.done", "answer", assistantMessage.Content)
		return assistantMessage.Content, nil
	}
	a.logger.Info(ctx, "agent.tool_calls.received", "count", len(assistantMessage.ToolCalls))

	messages = append(messages, assistantMessage)
	for _, call := range assistantMessage.ToolCalls {
		a.logger.Debug(ctx, "agent.tool.call", "name", call.Function.Name, "arguments", call.Function.Arguments)
		// 当前任务只支持单轮流程，但同一轮内允许模型并发式返回多个 tool call，需全部执行后再回填。
		toolMessage, err := a.runToolCall(ctx, call)
		if err != nil {
			a.logger.Error(ctx, "agent.tool.error", "name", call.Function.Name, "error", err)
			return "", err
		}
		a.logger.Debug(ctx, "agent.tool.result", "name", call.Function.Name, "content", toolMessage.Content)
		messages = append(messages, toolMessage)
	}

	a.logger.Debug(ctx, "agent.llm.final.request", "messages", len(messages))
	final, err := a.provider.Chat(ctx, llms.ChatRequest{
		Model:    a.model,
		Messages: messages,
	})
	if err != nil {
		a.logger.Error(ctx, "agent.llm.final.error", "error", err)
		return "", fmt.Errorf("final llm chat: %w", err)
	}
	if len(final.Message.ToolCalls) > 0 {
		a.logger.Error(ctx, "agent.llm.final.unsupported_tool_calls", "count", len(final.Message.ToolCalls))
		// 第二次对话必须直接给出最终答案；如果再次请求工具，说明模型试图进入当前实现不支持的第二轮 tool calling。
		return "", fmt.Errorf("final llm chat requested unsupported second tool-calling round (%d tool calls)", len(final.Message.ToolCalls))
	}

	a.logger.Info(ctx, "agent.run.done", "answer", final.Message.Content)
	return final.Message.Content, nil
}
