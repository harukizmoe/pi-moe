package llms

import (
	"context"
	"fmt"
)

// CollectChat 从 Provider stream 收集最终 assistant 消息；它只是同步调用的便捷视图，不是 Provider 的第二契约。
func CollectChat(ctx context.Context, provider Provider, req ChatRequest) (*ChatResponse, error) {
	stream, err := provider.ChatStream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("collect chat stream: %w", err)
	}

	for event := range stream {
		switch event.Type {
		case ChatStreamEventTypeDone:
			return &ChatResponse{Message: event.Message}, nil
		case ChatStreamEventTypeError:
			if event.Err == nil {
				return nil, fmt.Errorf("collect chat stream: error event without error")
			}
			return nil, fmt.Errorf("collect chat stream: %w", event.Err)
		}
	}

	return nil, fmt.Errorf("collect chat stream ended without done")
}
