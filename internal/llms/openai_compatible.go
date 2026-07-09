package llms

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOpenAICompatibleTimeout = 60
	maxOpenAIErrorBodyBytes        = 4096
)

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
	Stream   bool            `json:"stream,omitempty"`
}

type openAIMessage struct {
	Role string `json:"role"`
	// Content 使用指针配合 omitempty，才能区分“字段缺失”和“显式空字符串”。
	// 这样 tool result 的 Content == "" 仍会编码成 "content":""。
	Content    *string          `json:"content,omitempty"`
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

type openAIChatStreamResponse struct {
	Choices []openAIChatStreamChoice `json:"choices"`
	Error   *openAIStreamError       `json:"error"`
}

type openAIStreamError struct {
	Message string `json:"message"`
}

type openAIChatStreamChoice struct {
	Delta        openAIChatStreamDelta `json:"delta"`
	FinishReason string                `json:"finish_reason"`
}

type openAIChatStreamDelta struct {
	Role             string                    `json:"role"`
	Content          string                    `json:"content"`
	ReasoningContent string                    `json:"reasoning_content"`
	ToolCalls        []openAIChatToolCallDelta `json:"tool_calls"`
}

type openAIChatToolCallDelta struct {
	Index    int                    `json:"index"`
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIToolCallFunction `json:"function"`
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

// ChatStream 发送一次 /chat/completions streaming 请求，并把 SSE chunk 转回标准化事件。
func (p *OpenAICompatibleProvider) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error) {
	resp, err := p.doChatCompletions(ctx, req)
	if err != nil {
		return nil, err
	}

	events := make(chan ChatStreamEvent, 1)
	go func() {
		defer close(events)
		defer resp.Body.Close()

		reader := bufio.NewReader(resp.Body)
		role := RoleAssistant
		var content strings.Builder
		var toolCalls []openAIToolCall
		for {
			if err := ctx.Err(); err != nil {
				events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: fmt.Errorf("openai-compatible stream context: %w", err)}
				return
			}

			payload, err := readOpenAIStreamData(reader)
			if err != nil {
				if err == io.EOF {
					events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: fmt.Errorf("openai-compatible stream ended before completion")}
				} else {
					events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: fmt.Errorf("read openai-compatible stream: %w", err)}
				}
				return
			}
			if payload == "" {
				continue
			}
			if payload == "[DONE]" {
				if err := validateOpenAIStreamToolCalls(toolCalls); err != nil {
					events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: err}
					return
				}
				events <- ChatStreamEvent{Type: ChatStreamEventTypeDone, Message: openAIStreamMessage(role, content.String(), toolCalls)}
				return
			}

			var decoded openAIChatStreamResponse
			if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
				events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: fmt.Errorf("parse openai-compatible stream chunk: %w", err)}
				return
			}
			if decoded.Error != nil {
				events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: openAIStreamResponseError(decoded.Error)}
				return
			}
			if len(decoded.Choices) == 0 {
				continue
			}

			choice := decoded.Choices[0]
			delta := ChatStreamDelta{}
			if choice.Delta.Role != "" {
				role = Role(choice.Delta.Role)
				delta.Role = role
			}
			if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
				delta.Content = choice.Delta.Content
			}
			if choice.Delta.ReasoningContent != "" {
				delta.ReasoningContent = choice.Delta.ReasoningContent
			}
			if len(choice.Delta.ToolCalls) > 0 {
				toolCalls, err = mergeOpenAIStreamToolCalls(toolCalls, choice.Delta.ToolCalls, &delta)
				if err != nil {
					events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: err}
					return
				}
			}

			if delta.Content != "" || delta.ReasoningContent != "" || len(delta.ToolCalls) > 0 {
				events <- ChatStreamEvent{Type: ChatStreamEventTypeDelta, Delta: delta}
			}
			if choice.FinishReason != "" {
				if err := validateOpenAIStreamToolCalls(toolCalls); err != nil {
					events <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: err}
					return
				}
				events <- ChatStreamEvent{Type: ChatStreamEventTypeDone, Message: openAIStreamMessage(role, content.String(), toolCalls)}
				return
			}
		}
	}()

	return events, nil
}

func (p *OpenAICompatibleProvider) doChatCompletions(ctx context.Context, req ChatRequest) (*http.Response, error) {
	payload, err := json.Marshal(openAIChatRequest{
		Model:    firstNonEmpty(req.Model, p.model),
		Messages: toOpenAIMessages(req.Messages),
		Tools:    toOpenAITools(req.Tools),
		Stream:   true,
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
		return nil, fmt.Errorf("openai-compatible chat completions request: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		err := openAIStatusError(resp)
		resp.Body.Close()
		return nil, err
	}

	return resp, nil
}

func openAIStatusError(resp *http.Response) error {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxOpenAIErrorBodyBytes))
	if readErr != nil {
		return fmt.Errorf("openai-compatible chat completions failed: status %d: read error body: %w", resp.StatusCode, readErr)
	}
	bodyText := strings.TrimSpace(string(body))
	if bodyText == "" {
		bodyText = "<empty body>"
	}
	return fmt.Errorf("openai-compatible chat completions failed: status %d: %s", resp.StatusCode, bodyText)
}

func openAIStreamResponseError(streamErr *openAIStreamError) error {
	message := strings.TrimSpace(streamErr.Message)
	if message == "" {
		return fmt.Errorf("openai-compatible stream error")
	}
	return fmt.Errorf("openai-compatible stream error: %s", message)
}

func readOpenAIStreamData(reader *bufio.Reader) (string, error) {
	var dataLines []string

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}

		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")

		switch {
		case line == "":
			if len(dataLines) > 0 {
				return strings.Join(dataLines, "\n"), nil
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimPrefix(line, "data:")
			dataLines = append(dataLines, strings.TrimPrefix(data, " "))
		}

		if err == io.EOF {
			if len(dataLines) == 0 {
				return "", io.EOF
			}
			return strings.Join(dataLines, "\n"), nil
		}
	}
}

func mergeOpenAIStreamToolCalls(toolCalls []openAIToolCall, deltas []openAIChatToolCallDelta, eventDelta *ChatStreamDelta) ([]openAIToolCall, error) {
	if len(deltas) == 0 {
		return toolCalls, nil
	}
	if eventDelta.ToolCalls == nil {
		eventDelta.ToolCalls = make([]ToolCallDelta, 0, len(deltas))
	}

	for _, delta := range deltas {
		if delta.Index < 0 {
			return nil, fmt.Errorf("decode openai chat stream chunk: negative tool call index %d", delta.Index)
		}
		for len(toolCalls) <= delta.Index {
			toolCalls = append(toolCalls, openAIToolCall{})
		}

		toolCall := &toolCalls[delta.Index]
		if delta.ID != "" {
			toolCall.ID = delta.ID
		}
		if delta.Type != "" {
			toolCall.Type = delta.Type
		}
		if delta.Function.Name != "" {
			toolCall.Function.Name = delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			toolCall.Function.Arguments += delta.Function.Arguments
		}

		eventDelta.ToolCalls = append(eventDelta.ToolCalls, ToolCallDelta{
			Index: delta.Index,
			ID:    delta.ID,
			Type:  delta.Type,
			Function: ToolCallFunctionDelta{
				Name:      delta.Function.Name,
				Arguments: delta.Function.Arguments,
			},
		})
	}

	return toolCalls, nil
}

func validateOpenAIStreamToolCalls(toolCalls []openAIToolCall) error {
	for i, toolCall := range toolCalls {
		if strings.TrimSpace(toolCall.ID) == "" {
			return fmt.Errorf("openai-compatible tool call missing id at index %d", i)
		}
		if strings.TrimSpace(toolCall.Function.Name) == "" {
			return fmt.Errorf("openai-compatible tool call missing function name at index %d", i)
		}
		arguments := strings.TrimSpace(toolCall.Function.Arguments)
		if arguments == "" {
			continue
		}
		if !json.Valid([]byte(arguments)) {
			return fmt.Errorf("openai-compatible tool call arguments are not valid JSON at index %d", i)
		}
	}
	return nil
}

func openAIStreamMessage(role Role, content string, toolCalls []openAIToolCall) Message {
	if role == "" {
		role = RoleAssistant
	}
	return Message{
		Role:      role,
		Content:   content,
		ToolCalls: openAIStreamToolCalls(toolCalls),
	}
}

func openAIStreamToolCalls(toolCalls []openAIToolCall) []ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}

	compact := make([]openAIToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		if toolCall.ID == "" && toolCall.Type == "" && toolCall.Function.Name == "" && toolCall.Function.Arguments == "" {
			continue
		}
		compact = append(compact, toolCall)
	}
	return fromOpenAIToolCalls(compact)
}

func toOpenAIMessages(messages []Message) []openAIMessage {
	if len(messages) == 0 {
		return nil
	}

	out := make([]openAIMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, openAIMessage{
			Role:       string(msg.Role),
			Content:    openAIContent(msg.Content),
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
		Content:    fromOpenAIContent(msg.Content),
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
		callType := call.Type
		if callType == "" {
			callType = "function"
		}
		out = append(out, ToolCall{
			ID:   call.ID,
			Type: callType,
			Function: ToolCallFunction{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}

func openAIContent(content string) *string {
	return &content
}

func fromOpenAIContent(content *string) string {
	if content == nil {
		return ""
	}
	return *content
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
