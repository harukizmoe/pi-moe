package agent

import (
	"context"
	"fmt"

	"harukizmoe/pimoe/internal/llms"
)

// runToolCall 负责把一次模型 tool call 转发给本地工具注册表，并封装回标准 tool 消息。
func (a *Agent) runToolCall(ctx context.Context, call llms.ToolCall) (llms.Message, error) {
	result, err := a.tools.Call(ctx, call.Function.Name, call.Function.Arguments)
	if err != nil {
		return llms.Message{}, fmt.Errorf("call tool %q: %w", call.Function.Name, err)
	}

	return llms.Message{
		Role:       llms.RoleTool,
		ToolCallID: call.ID,
		Content:    result,
	}, nil
}
