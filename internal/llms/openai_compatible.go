package llms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const defaultOpenAICompatibleTimeout = 60

// OpenAICompatibleProvider 负责把标准化 llms 请求转换到 OpenAI-compatible /chat/completions。
type OpenAICompatibleProvider struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

type openAIChatRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type openAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIToolCallFunction `json:"function"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIChatResponse struct {
	Choices []openAIChoice `json:"choices"`
}

type openAIChoice struct {
	Message openAIMessage `json:"message"`
}

// NewOpenAICompatibleProvider 创建只负责协议适配的 OpenAI-compatible Provider。
func NewOpenAICompatibleProvider(cfg ProviderConfig) (Provider, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("openai-compatible base_url is required")
	}

	timeout := cfg.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultOpenAICompatibleTimeout
	}

	return &OpenAICompatibleProvider{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		client:  &http.Client{Timeout: time.Duration(timeout) * time.Second},
	}, nil
}

// Chat 发送一次 /chat/completions 请求，并把首个 choice 转回标准化 assistant 消息。
func (p *OpenAICompatibleProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	payload, err := json.Marshal(openAIChatRequest{
		Model:    firstNonEmpty(req.Model, p.model),
		Messages: toOpenAIMessages(req.Messages),
		Tools:    toOpenAITools(req.Tools),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal openai chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create openai chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send openai chat request: %w", err)
	}
	defer resp.Body.Close()

	// Provider 只验证协议边界，任何非 2xx 都直接上抛给调用方处理。
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("openai chat returned status %d", resp.StatusCode)
	}

	var decoded openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode openai chat response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return nil, fmt.Errorf("openai chat returned empty choices")
	}

	return &ChatResponse{Message: fromOpenAIMessage(decoded.Choices[0].Message)}, nil
}

func toOpenAIMessages(messages []Message) []openAIMessage {
	if len(messages) == 0 {
		return nil
	}

	out := make([]openAIMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, openAIMessage{
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCalls:  toOpenAIToolCalls(msg.ToolCalls),
			ToolCallID: msg.ToolCallID,
		})
	}
	return out
}

func toOpenAITools(tools []Tool) []openAITool {
	if len(tools) == 0 {
		return nil
	}

	out := make([]openAITool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, openAITool{
			Type: tool.Type,
			Function: openAIToolFunction{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  tool.Function.Parameters,
			},
		})
	}
	return out
}

func toOpenAIToolCalls(calls []ToolCall) []openAIToolCall {
	if len(calls) == 0 {
		return nil
	}

	out := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, openAIToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: openAIToolCallFunction{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}

func fromOpenAIMessage(msg openAIMessage) Message {
	return Message{
		Role:       Role(msg.Role),
		Content:    msg.Content,
		ToolCalls:  fromOpenAIToolCalls(msg.ToolCalls),
		ToolCallID: msg.ToolCallID,
	}
}

func fromOpenAIToolCalls(calls []openAIToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}

	out := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, ToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: ToolCallFunction{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
