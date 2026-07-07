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

// Chat 第一次返回固定 calculator tool call，随后把最近的 tool result 转成最终回答。
func (p *FakeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// 如果 agent 已经执行过工具，则用该工具结果结束 fake 对话。
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == RoleTool {
			return &ChatResponse{Message: Message{Role: RoleAssistant, Content: "13 * 7 = " + msg.Content}}, nil
		}
	}

	// 否则请求 calculator 工具，并使用稳定参数保证测试确定性。
	return &ChatResponse{Message: Message{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{{
			ID:   "call_fake_calculator",
			Type: "function",
			Function: ToolCallFunction{
				Name:      "calculator",
				Arguments: `{"a":13,"b":7,"op":"mul"}`,
			},
		}},
	}}, nil
}

// ChatStream 复用 Chat 的确定性结果，并以 provider-neutral streaming 事件返回。
func (p *FakeProvider) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return fakeStreamError(err), nil
	}

	resp, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return fakeStreamError(err), nil
	}

	events := make(chan ChatStreamEvent, 3)
	if resp.Message.Content != "" {
		events <- ChatStreamEvent{
			Type: ChatStreamEventTypeDelta,
			Delta: ChatStreamDelta{
				Role:    resp.Message.Role,
				Content: resp.Message.Content,
			},
		}
	}
	if len(resp.Message.ToolCalls) > 0 {
		events <- ChatStreamEvent{
			Type:  ChatStreamEventTypeDelta,
			Delta: fakeToolCallDelta(resp.Message),
		}
	}
	events <- ChatStreamEvent{Type: ChatStreamEventTypeDone, Message: resp.Message}
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
