package llms

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatibleProviderSendsChatCompletionPayload(t *testing.T) {
	type capturedToolCall struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	type capturedMessage struct {
		Role       string             `json:"role"`
		Content    string             `json:"content,omitempty"`
		ToolCalls  []capturedToolCall `json:"tool_calls,omitempty"`
		ToolCallID string             `json:"tool_call_id,omitempty"`
	}
	type capturedTool struct {
		Type     string `json:"type"`
		Function struct {
			Name        string         `json:"name"`
			Description string         `json:"description,omitempty"`
			Parameters  map[string]any `json:"parameters"`
		} `json:"function"`
	}
	type capturedRequest struct {
		Model    string            `json:"model"`
		Messages []capturedMessage `json:"messages"`
		Tools    []capturedTool    `json:"tools,omitempty"`
	}

	var captured capturedRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content-type = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": "calling calculator",
        "tool_calls": [
          {
            "id": "call_1",
            "type": "function",
            "function": {
              "name": "calculator",
              "arguments": "{\"a\":13,\"b\":7,\"op\":\"mul\"}"
            }
          }
        ]
      }
    }
  ]
}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(ProviderConfig{
		BaseURL:        server.URL + "/v1/",
		APIKey:         "test-key",
		Model:          "test-model",
		TimeoutSeconds: 3,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	resp, err := provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: RoleSystem, Content: "You are a calculator."},
			{Role: RoleUser, Content: "calculate"},
			{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{{
					ID:   "call_prev",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "calculator",
						Arguments: `{"a":1,"b":2,"op":"add"}`,
					},
				}},
			},
			{Role: RoleTool, ToolCallID: "call_prev", Content: "3"},
		},
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:        "calculator",
				Description: "Calculate two numbers.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a": map[string]any{"type": "number"},
					},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if captured.Model != "test-model" {
		t.Fatalf("model = %q", captured.Model)
	}
	if len(captured.Messages) != 4 {
		t.Fatalf("messages len = %d", len(captured.Messages))
	}
	if captured.Messages[0].Role != string(RoleSystem) || captured.Messages[0].Content != "You are a calculator." {
		t.Fatalf("system message = %#v", captured.Messages[0])
	}
	if len(captured.Messages[2].ToolCalls) != 1 {
		t.Fatalf("assistant tool calls len = %d", len(captured.Messages[2].ToolCalls))
	}
	if captured.Messages[2].ToolCalls[0].Function.Arguments != `{"a":1,"b":2,"op":"add"}` {
		t.Fatalf("assistant tool arguments = %q", captured.Messages[2].ToolCalls[0].Function.Arguments)
	}
	if captured.Messages[3].Role != string(RoleTool) || captured.Messages[3].ToolCallID != "call_prev" || captured.Messages[3].Content != "3" {
		t.Fatalf("tool message = %#v", captured.Messages[3])
	}
	if len(captured.Tools) != 1 {
		t.Fatalf("tools len = %d", len(captured.Tools))
	}
	if captured.Tools[0].Function.Name != "calculator" {
		t.Fatalf("tool name = %q", captured.Tools[0].Function.Name)
	}

	if resp.Message.Role != RoleAssistant {
		t.Fatalf("response role = %q", resp.Message.Role)
	}
	if resp.Message.Content != "calling calculator" {
		t.Fatalf("response content = %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("tool name = %q", resp.Message.ToolCalls[0].Function.Name)
	}
}
func TestOpenAICompatibleProviderPreservesEmptyToolContent(t *testing.T) {
	var payload string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		payload = string(body)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 3})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	_, err = provider.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleTool, ToolCallID: "call_empty", Content: ""}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if !strings.Contains(payload, `"role":"tool"`) {
		t.Fatalf("payload missing tool role: %s", payload)
	}
	if !strings.Contains(payload, `"tool_call_id":"call_empty"`) {
		t.Fatalf("payload missing tool_call_id: %s", payload)
	}
	if !strings.Contains(payload, `"content":""`) {
		t.Fatalf("payload missing empty content field: %s", payload)
	}
}

func TestOpenAICompatibleProviderStatusErrorIncludesBodyExcerpt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream exploded","request_id":"req_123"}}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 3})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	_, err = provider.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("Chat() error = nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error missing status: %v", err)
	}
	if !strings.Contains(err.Error(), "upstream exploded") {
		t.Fatalf("error missing body excerpt: %v", err)
	}
}

func TestOpenAICompatibleProviderReturnsDecodeErrorOnMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":]}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 3})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	_, err = provider.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("Chat() error = nil")
	}
	if !strings.Contains(err.Error(), "decode openai chat response") {
		t.Fatalf("error missing decode context: %v", err)
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("error missing wrapped syntax error: %v", err)
	}
}

func TestOpenAICompatibleProviderNormalizesMissingToolCallTypeToFunction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [
				{
					"message": {
						"role": "assistant",
						"tool_calls": [
							{
								"id": "call_1",
								"function": {
									"name": "calculator",
									"arguments": "{\"a\":13,\"b\":7,\"op\":\"mul\"}"
								}
							}
						]
					}
				}
			]
		}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 3})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	resp, err := provider.Chat(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].Type != "function" {
		t.Fatalf("tool call type = %q", resp.Message.ToolCalls[0].Type)
	}
	if resp.Message.ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("tool name = %q", resp.Message.ToolCalls[0].Function.Name)
	}
	if resp.Message.ToolCalls[0].Function.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("tool arguments = %q", resp.Message.ToolCalls[0].Function.Arguments)
	}
	if resp.Message.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool call id = %q", resp.Message.ToolCalls[0].ID)
	}
	if resp.Message.Role != RoleAssistant {
		t.Fatalf("response role = %q", resp.Message.Role)
	}
}

func TestOpenAICompatibleProviderReturnsErrorOnEmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 3})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	_, err = provider.Chat(context.Background(), ChatRequest{})
	if err == nil {
		t.Fatal("Chat() error = nil")
	}
	if !strings.Contains(err.Error(), "empty choices") {
		t.Fatalf("error = %v", err)
	}
}
