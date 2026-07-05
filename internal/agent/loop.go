package agent

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

// Run 执行一次最多一轮的 tool calling：先请求模型，再执行工具，最后回填工具结果获取最终答案。
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	messages := []llms.Message{{Role: llms.RoleUser, Content: input}}

	// 第一次对话把可用工具 schema 暴露给模型，让其决定是否发起 tool call。
	first, err := a.provider.Chat(ctx, llms.ChatRequest{
		Model:    a.model,
		Messages: messages,
		Tools:    a.tools.Schemas(),
	})
	if err != nil {
		return "", fmt.Errorf("first llm chat: %w", err)
	}

	assistantMessage := first.Message
	if len(assistantMessage.ToolCalls) == 0 {
		return assistantMessage.Content, nil
	}

	messages = append(messages, assistantMessage)
	for _, call := range assistantMessage.ToolCalls {
		// 当前任务只支持单轮流程，但同一轮内允许模型并发式返回多个 tool call，需全部执行后再回填。
		toolMessage, err := a.runToolCall(ctx, call)
		if err != nil {
			return "", err
		}
		messages = append(messages, toolMessage)
	}

	final, err := a.provider.Chat(ctx, llms.ChatRequest{
		Model:    a.model,
		Messages: messages,
	})
	if err != nil {
		return "", fmt.Errorf("final llm chat: %w", err)
	}

	return final.Message.Content, nil
}
