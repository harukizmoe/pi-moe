package agent

import (
	"fmt"
	"strings"

	"harukizmoe/pimoe/internal/llms"
)

// Message 表示 Agent 内部使用的强语义对话消息。
type Message interface {
	agentMessage()
}

// UserMessage 表示用户输入消息。
type UserMessage struct {
	// Content 是发送给模型的用户文本；转换前会裁剪首尾空白。
	Content string
}

func (UserMessage) agentMessage() {}

// AssistantMessage 表示模型响应消息，可包含最终文本或 tool calls。
type AssistantMessage struct {
	// Content 是模型返回的可见文本。
	Content string
	// ToolCalls 是模型请求执行的本地工具调用。
	ToolCalls []llms.ToolCall
}

func (AssistantMessage) agentMessage() {}

// ToolResultMessage 表示一次本地工具执行后返回给模型的结果。
type ToolResultMessage struct {
	// ToolCallID 关联模型发起的 assistant tool call。
	ToolCallID string
	// ToolName 是被执行的本地工具名，用于 trace、日志和错误摘要。
	ToolName string
	// Content 是发送给模型的工具结果文本；失败时必须是安全错误摘要。
	Content string
	// IsError 标记该工具结果是否表示执行失败。
	IsError bool
}

func (ToolResultMessage) agentMessage() {}

func toLLMMessages(messages []Message) ([]llms.Message, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}

	out := make([]llms.Message, 0, len(messages))
	var pendingToolCalls []llms.ToolCall
	for i, message := range messages {
		converted, err := toLLMMessage(message)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		if err := validateToolResultOrder(i, converted, pendingToolCalls); err != nil {
			return nil, err
		}

		switch converted.Role {
		case llms.RoleAssistant:
			pendingToolCalls = converted.ToolCalls
		case llms.RoleTool:
			pendingToolCalls = pendingToolCalls[1:]
		}
		out = append(out, converted)
	}
	if len(pendingToolCalls) > 0 {
		return nil, fmt.Errorf("messages ended with missing tool result for pending assistant tool call %q", pendingToolCalls[0].ID)
	}
	return out, nil
}

func toLLMMessagesWithSystemPrompt(messages []Message, systemPrompt string) ([]llms.Message, error) {
	converted, err := toLLMMessages(messages)
	if err != nil {
		return nil, err
	}
	prompt := strings.TrimSpace(systemPrompt)
	if prompt == "" {
		return converted, nil
	}
	out := make([]llms.Message, 0, len(converted)+1)
	out = append(out, llms.Message{Role: llms.RoleSystem, Content: prompt})
	out = append(out, converted...)
	return out, nil
}

func validateToolResultOrder(index int, message llms.Message, pendingToolCalls []llms.ToolCall) error {
	switch message.Role {
	case llms.RoleAssistant:
		if len(pendingToolCalls) > 0 {
			return fmt.Errorf("message %d: missing tool result for pending assistant tool call %q before assistant message", index, pendingToolCalls[0].ID)
		}
	case llms.RoleTool:
		if len(pendingToolCalls) == 0 {
			return fmt.Errorf("message %d: tool result %q has no pending assistant tool call", index, message.ToolCallID)
		}
		if message.ToolCallID != pendingToolCalls[0].ID {
			return fmt.Errorf("message %d: tool result id mismatch: got %q, want pending assistant tool call %q", index, message.ToolCallID, pendingToolCalls[0].ID)
		}
	default:
		if len(pendingToolCalls) > 0 {
			return fmt.Errorf("message %d: missing tool result for pending assistant tool call %q before %s message", index, pendingToolCalls[0].ID, message.Role)
		}
	}
	return nil
}

func toLLMMessage(message Message) (llms.Message, error) {
	switch msg := message.(type) {
	case UserMessage:
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			return llms.Message{}, fmt.Errorf("user message content must not be empty")
		}
		return llms.Message{Role: llms.RoleUser, Content: content}, nil
	case AssistantMessage:
		toolCalls := append([]llms.ToolCall(nil), msg.ToolCalls...)
		if strings.TrimSpace(msg.Content) == "" && len(toolCalls) == 0 {
			return llms.Message{}, fmt.Errorf("assistant message must have content or tool calls")
		}
		for _, call := range toolCalls {
			if strings.TrimSpace(call.ID) == "" {
				return llms.Message{}, fmt.Errorf("assistant tool call must have id")
			}
			if strings.TrimSpace(call.Function.Name) == "" {
				return llms.Message{}, fmt.Errorf("assistant tool call must have function name")
			}
		}
		return llms.Message{Role: llms.RoleAssistant, Content: msg.Content, ToolCalls: toolCalls}, nil
	case ToolResultMessage:
		if strings.TrimSpace(msg.ToolCallID) == "" {
			return llms.Message{}, fmt.Errorf("tool result message must have tool call id")
		}
		if strings.TrimSpace(msg.ToolName) == "" {
			return llms.Message{}, fmt.Errorf("tool result message must have tool name")
		}
		return llms.Message{Role: llms.RoleTool, ToolCallID: msg.ToolCallID, Content: msg.Content}, nil
	default:
		return llms.Message{}, fmt.Errorf("unsupported agent message type %T", message)
	}
}
