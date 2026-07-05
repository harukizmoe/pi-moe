package llms

import "context"

type FakeProvider struct {
	model string
}

func NewFakeProvider(cfg ProviderConfig) (Provider, error) {
	return &FakeProvider{model: cfg.Model}, nil
}

func (p *FakeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == RoleTool {
			return &ChatResponse{Message: Message{Role: RoleAssistant, Content: "13 * 7 = " + msg.Content}}, nil
		}
	}

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