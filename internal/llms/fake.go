package llms

import "context"

// FakeProvider 是用于无网络测试 tool calling 的确定性 Provider。
type FakeProvider struct {
	model string
}

// NewFakeProvider 创建用于本地测试和 CLI smoke check 的 fake Provider。
func NewFakeProvider(cfg ProviderConfig) (Provider, error) {
	return &FakeProvider{model: cfg.Model}, nil
}

func (p *FakeProvider) fakeChatMessage(req ChatRequest) Message {
	// 如果 agent 已经执行过工具，则用该工具结果结束 fake 对话。
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == RoleTool {
			return Message{Role: RoleAssistant, Content: "13 * 7 = " + msg.Content}
		}
	}

	// 否则请求 calculator 工具，并使用稳定参数保证测试确定性。
	return Message{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{{
			ID:   "call_fake_calculator",
			Type: "function",
			Function: ToolCallFunction{
				Name:      "calculator",
				Arguments: `{"a":13,"b":7,"op":"mul"}`,
			},
		}},
	}
}

// ChatStream 返回确定性的 fake provider-neutral streaming 事件。
func (p *FakeProvider) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return fakeStreamError(err), nil
	}

	message := p.fakeChatMessage(req)
	if err := ctx.Err(); err != nil {
		return fakeStreamError(err), nil
	}

	events := make(chan ChatStreamEvent, 3)
	if message.Content != "" {
		events <- ChatStreamEvent{
			Type: ChatStreamEventTypeDelta,
			Delta: ChatStreamDelta{
				Role:    message.Role,
				Content: message.Content,
			},
		}
	}
	if len(message.ToolCalls) > 0 {
		events <- ChatStreamEvent{
			Type:  ChatStreamEventTypeDelta,
			Delta: fakeToolCallDelta(message),
		}
	}
	events <- ChatStreamEvent{Type: ChatStreamEventTypeDone, Message: message}
	close(events)
	return events, nil
}

func fakeStreamError(err error) <-chan ChatStreamEvent {
	events := make(chan ChatStreamEvent, 1)
	events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: err}
	close(events)
	return events
}

func fakeToolCallDelta(msg Message) ChatStreamDelta {
	delta := ChatStreamDelta{
		Role:      msg.Role,
		ToolCalls: make([]ToolCallDelta, 0, len(msg.ToolCalls)),
	}
	for i, call := range msg.ToolCalls {
		delta.ToolCalls = append(delta.ToolCalls, ToolCallDelta{
			Index: i,
			ID:    call.ID,
			Type:  call.Type,
			Function: ToolCallFunctionDelta{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return delta
}
