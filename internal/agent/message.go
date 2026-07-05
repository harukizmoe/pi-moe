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
	for i, message := range messages {
		converted, err := toLLMMessage(message)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		out = append(out, converted)
	}
	return out, nil
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

func fromLLMMessages(messages []llms.Message) ([]Message, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}

	toolNamesByID := make(map[string]string)
	out := make([]Message, 0, len(messages))
	for i, message := range messages {
		converted, err := fromLLMMessage(message, toolNamesByID)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		out = append(out, converted)
	}
	return out, nil
}

func fromLLMMessage(message llms.Message, toolNamesByID map[string]string) (Message, error) {
	switch message.Role {
	case llms.RoleUser:
		if len(message.ToolCalls) > 0 || message.ToolCallID != "" {
			return nil, fmt.Errorf("user message cannot have tool calls or tool call id")
		}
		return UserMessage{Content: message.Content}, nil
	case llms.RoleAssistant:
		if message.ToolCallID != "" {
			return nil, fmt.Errorf("assistant message cannot have tool call id")
		}
		toolCalls := append([]llms.ToolCall(nil), message.ToolCalls...)
		if strings.TrimSpace(message.Content) == "" && len(toolCalls) == 0 {
			return nil, fmt.Errorf("assistant message must have content or tool calls")
		}
		for _, call := range toolCalls {
			if call.ID == "" {
				return nil, fmt.Errorf("assistant tool call must have id")
			}
			if call.Function.Name == "" {
				return nil, fmt.Errorf("assistant tool call must have function name")
			}
			toolNamesByID[call.ID] = call.Function.Name
		}
		return AssistantMessage{Content: message.Content, ToolCalls: toolCalls}, nil
	case llms.RoleTool:
		if len(message.ToolCalls) > 0 {
			return nil, fmt.Errorf("tool message cannot have tool calls")
		}
		if message.ToolCallID == "" {
			return nil, fmt.Errorf("tool message must have tool call id")
		}
		toolName, ok := toolNamesByID[message.ToolCallID]
		if !ok {
			return nil, fmt.Errorf("tool message must match a previous assistant tool call")
		}
		return ToolResultMessage{ToolCallID: message.ToolCallID, ToolName: toolName, Content: message.Content}, nil
	default:
		return nil, fmt.Errorf("unsupported message role %q", message.Role)
	}
}
