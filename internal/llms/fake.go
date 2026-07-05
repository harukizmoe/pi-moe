package llms

import "context"

// FakeProvider is a deterministic provider used to test tool calling without network access.
type FakeProvider struct {
	model string
}

// NewFakeProvider creates a fake provider for local tests and CLI smoke checks.
func NewFakeProvider(cfg ProviderConfig) (Provider, error) {
	return &FakeProvider{model: cfg.Model}, nil
}

// Chat returns a fixed calculator tool call first, then turns the latest tool result into a final answer.
func (p *FakeProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// If the agent already executed a tool, finish the fake conversation with that result.
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == RoleTool {
			return &ChatResponse{Message: Message{Role: RoleAssistant, Content: "13 * 7 = " + msg.Content}}, nil
		}
	}

	// Otherwise request the calculator tool with stable arguments so tests stay deterministic.
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
