package llms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestOpenAICompatibleProviderChatStreamSendsStreamingPayloadAndParsesText(t *testing.T) {
	type capturedMessage struct {
		Role    string `json:"role"`
		Content string `json:"content,omitempty"`
	}
	type capturedRequest struct {
		Model    string            `json:"model"`
		Stream   bool              `json:"stream"`
		Messages []capturedMessage `json:"messages"`
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

		writeSSE(t, w,
			`{"choices":[{"delta":{"role":"assistant"}}]}`,
			`{"choices":[{"delta":{"content":"hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"finish_reason":"stop"}]}`,
			`[DONE]`,
		)
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

	stream, err := requireStreamingProvider(t, provider).ChatStream(context.Background(), ChatRequest{
		Messages: []Message{
			{Role: RoleSystem, Content: "You are a calculator."},
			{Role: RoleUser, Content: "say hello"},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	events := collectChatStreamEvents(t, stream)

	if !captured.Stream {
		t.Fatal("stream = false")
	}
	if captured.Model != "test-model" {
		t.Fatalf("model = %q", captured.Model)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages len = %d", len(captured.Messages))
	}
	if captured.Messages[0].Role != string(RoleSystem) || captured.Messages[0].Content != "You are a calculator." {
		t.Fatalf("system message = %#v", captured.Messages[0])
	}
	if captured.Messages[1].Role != string(RoleUser) || captured.Messages[1].Content != "say hello" {
		t.Fatalf("user message = %#v", captured.Messages[1])
	}

	var deltas []string
	var done *ChatStreamEvent
	for _, event := range events {
		switch event.Type {
		case ChatStreamEventTypeDelta:
			if event.Delta.Content != "" {
				deltas = append(deltas, event.Delta.Content)
			}
		case ChatStreamEventTypeDone:
			if done != nil {
				t.Fatalf("multiple done events: %#v", events)
			}
			eventCopy := event
			done = &eventCopy
		case ChatStreamEventTypeError:
			t.Fatalf("unexpected error event: %v", event.Err)
		default:
			t.Fatalf("unexpected event type = %q", event.Type)
		}
	}

	if len(deltas) != 2 || deltas[0] != "hel" || deltas[1] != "lo" {
		t.Fatalf("content deltas = %#v", deltas)
	}
	if done == nil {
		t.Fatal("missing done event")
	}
	if done.Message.Role != RoleAssistant {
		t.Fatalf("done role = %q", done.Message.Role)
	}
	if done.Message.Content != "hello" {
		t.Fatalf("done content = %q", done.Message.Content)
	}
	if len(done.Message.ToolCalls) != 0 {
		t.Fatalf("unexpected tool calls in done message: %#v", done.Message.ToolCalls)
	}
}

func TestOpenAICompatibleProviderChatStreamAggregatesToolCallChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w,
			`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":1,"id":"call_weather","function":{"name":"weather","arguments":"{\"city\":\"To"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_calc","function":{"name":"calculator","arguments":"{\"a\":13"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"kyo\",\"unit\":\"C\"}"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":",\"b\":7"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":",\"op\":\"mul\"}"}}]}}]}`,
			`{"choices":[{"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 3})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	stream, err := requireStreamingProvider(t, provider).ChatStream(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	events := collectChatStreamEvents(t, stream)

	deltaArgumentsByIndex := map[int]string{}
	deltaIDsByIndex := map[int]string{}
	deltaNamesByIndex := map[int]string{}
	var done *ChatStreamEvent
	for _, event := range events {
		switch event.Type {
		case ChatStreamEventTypeDelta:
			for _, toolCall := range event.Delta.ToolCalls {
				deltaArgumentsByIndex[toolCall.Index] += toolCall.Function.Arguments
				if toolCall.ID != "" {
					deltaIDsByIndex[toolCall.Index] = toolCall.ID
				}
				if toolCall.Function.Name != "" {
					deltaNamesByIndex[toolCall.Index] = toolCall.Function.Name
				}
			}
		case ChatStreamEventTypeDone:
			eventCopy := event
			done = &eventCopy
		case ChatStreamEventTypeError:
			t.Fatalf("unexpected error event: %v", event.Err)
		}
	}

	if len(deltaArgumentsByIndex) != 2 {
		t.Fatalf("delta tool call indexes = %#v", deltaArgumentsByIndex)
	}
	if deltaArgumentsByIndex[0] != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("tool call index 0 delta arguments = %q", deltaArgumentsByIndex[0])
	}
	if deltaArgumentsByIndex[1] != `{"city":"Tokyo","unit":"C"}` {
		t.Fatalf("tool call index 1 delta arguments = %q", deltaArgumentsByIndex[1])
	}
	if deltaIDsByIndex[0] != "call_calc" {
		t.Fatalf("tool call index 0 delta id = %q", deltaIDsByIndex[0])
	}
	if deltaIDsByIndex[1] != "call_weather" {
		t.Fatalf("tool call index 1 delta id = %q", deltaIDsByIndex[1])
	}
	if deltaNamesByIndex[0] != "calculator" {
		t.Fatalf("tool call index 0 delta name = %q", deltaNamesByIndex[0])
	}
	if deltaNamesByIndex[1] != "weather" {
		t.Fatalf("tool call index 1 delta name = %q", deltaNamesByIndex[1])
	}
	if done == nil {
		t.Fatal("missing done event")
	}
	if done.Message.Role != RoleAssistant {
		t.Fatalf("done role = %q", done.Message.Role)
	}
	if len(done.Message.ToolCalls) != 2 {
		t.Fatalf("done tool calls len = %d", len(done.Message.ToolCalls))
	}
	if done.Message.ToolCalls[0].ID != "call_calc" {
		t.Fatalf("done tool call index 0 id = %q", done.Message.ToolCalls[0].ID)
	}
	if done.Message.ToolCalls[0].Type != "function" {
		t.Fatalf("done tool call index 0 type = %q", done.Message.ToolCalls[0].Type)
	}
	if done.Message.ToolCalls[0].Function.Name != "calculator" {
		t.Fatalf("done tool call index 0 name = %q", done.Message.ToolCalls[0].Function.Name)
	}
	if done.Message.ToolCalls[0].Function.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("done tool call index 0 arguments = %q", done.Message.ToolCalls[0].Function.Arguments)
	}
	if done.Message.ToolCalls[1].ID != "call_weather" {
		t.Fatalf("done tool call index 1 id = %q", done.Message.ToolCalls[1].ID)
	}
	if done.Message.ToolCalls[1].Type != "function" {
		t.Fatalf("done tool call index 1 type = %q", done.Message.ToolCalls[1].Type)
	}
	if done.Message.ToolCalls[1].Function.Name != "weather" {
		t.Fatalf("done tool call index 1 name = %q", done.Message.ToolCalls[1].Function.Name)
	}
	if done.Message.ToolCalls[1].Function.Arguments != `{"city":"Tokyo","unit":"C"}` {
		t.Fatalf("done tool call index 1 arguments = %q", done.Message.ToolCalls[1].Function.Arguments)
	}
}

func TestOpenAICompatibleProviderChatStreamSurfacesStatusAndMalformedStreamErrors(t *testing.T) {
	tests := []struct {
		name string
		handler http.HandlerFunc
		assert func(t *testing.T, err error, events []ChatStreamEvent)
	}{
		{
			name: "status_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("upstream exploded"))
			},
			assert: func(t *testing.T, err error, events []ChatStreamEvent) {
				t.Helper()
				if err == nil {
					t.Fatal("ChatStream() error = nil")
				}
				if len(events) != 0 {
					t.Fatalf("unexpected events on sync error: %#v", events)
				}
				if !strings.Contains(err.Error(), "500") {
					t.Fatalf("error missing status: %v", err)
				}
				if !strings.Contains(err.Error(), "upstream exploded") {
					t.Fatalf("error missing body excerpt: %v", err)
				}
			},
		},
		{
			name: "malformed_chunk",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeSSE(t, w, `{"choices":[}`)
			},
			assert: func(t *testing.T, err error, events []ChatStreamEvent) {
				t.Helper()
				if err != nil {
					t.Fatalf("ChatStream() error = %v", err)
				}

				var errorText string
				var sawDone bool
				for _, event := range events {
					switch event.Type {
					case ChatStreamEventTypeDone:
						sawDone = true
					case ChatStreamEventTypeError:
						if event.Err == nil {
							t.Fatal("error event err = nil")
						}
						errorText = event.Err.Error()
					}
				}

				if errorText == "" {
					t.Fatalf("events missing error event: %#v", events)
				}
				if !strings.Contains(errorText, "decode") && !strings.Contains(errorText, "unmarshal") {
					t.Fatalf("error missing decode context: %q", errorText)
				}
				if sawDone {
					t.Fatalf("unexpected done event: %#v", events)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.handler))
			defer server.Close()

			provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 3})
			if err != nil {
				t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
			}

			stream, err := requireStreamingProvider(t, provider).ChatStream(context.Background(), ChatRequest{})
			var events []ChatStreamEvent
			if err == nil {
				events = collectChatStreamEvents(t, stream)
			}

			tt.assert(t, err, events)
		})
	}
}

func TestOpenAICompatibleProviderChatStreamReturnsErrorWhenStreamEndsWithoutDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(t, w, `{"choices":[{"delta":{"content":"partial"}}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL, TimeoutSeconds: 3})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	stream, err := requireStreamingProvider(t, provider).ChatStream(context.Background(), ChatRequest{})
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	events := collectChatStreamEvents(t, stream)

	var sawDone bool
	var errorText string
	for _, event := range events {
		switch event.Type {
		case ChatStreamEventTypeDone:
			sawDone = true
		case ChatStreamEventTypeError:
			if event.Err == nil {
				t.Fatal("error event err = nil")
			}
			errorText = event.Err.Error()
		}
	}

	if sawDone {
		t.Fatalf("unexpected done event: %#v", events)
	}
	if !strings.Contains(errorText, "ended without done") {
		t.Fatalf("error missing ended without done: %#v", events)
	}
}

func requireStreamingProvider(t *testing.T, provider Provider) StreamingProvider {
	t.Helper()

	streamingProvider, ok := provider.(StreamingProvider)
	if !ok {
		t.Fatal("provider does not implement StreamingProvider")
	}

	return streamingProvider
}

func collectChatStreamEvents(t *testing.T, stream <-chan ChatStreamEvent) []ChatStreamEvent {
	t.Helper()

	var events []ChatStreamEvent
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	for {
		select {
		case event, ok := <-stream:
			if !ok {
				return events
			}
			events = append(events, event)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(2 * time.Second)
		case <-timer.C:
			t.Fatal("timed out waiting for chat stream events")
		}
	}
}

func writeSSE(t *testing.T, w http.ResponseWriter, payloads ...string) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("response writer does not support flushing")
	}

	for _, payload := range payloads {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			t.Fatalf("write sse payload: %v", err)
		}
		flusher.Flush()
	}
}
