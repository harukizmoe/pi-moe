package agent

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

// runToolCall 负责把一次模型 tool call 转发给本地工具注册表，并封装回 Agent tool result 消息。
func (a *Agent) runToolCall(ctx context.Context, call llms.ToolCall) (ToolResultMessage, error) {
	result, err := a.tools.Call(ctx, call.Function.Name, call.Function.Arguments)
	if err != nil {
		return ToolResultMessage{
			ToolCallID: call.ID,
			ToolName:   call.Function.Name,
			Content:    safeToolErrorContent(call.Function.Name),
			IsError:    true,
		}, fmt.Errorf("call tool %q: %w", call.Function.Name, err)
	}

	return ToolResultMessage{
		ToolCallID: call.ID,
		ToolName:   call.Function.Name,
		Content:    result,
	}, nil
}

func safeToolErrorContent(toolName string) string {
	return fmt.Sprintf("tool %q failed", toolName)
}
