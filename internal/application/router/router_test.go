package router_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	appdata "harukizmoe/pimoe/internal/application/data"
	approuter "harukizmoe/pimoe/internal/application/router"
	appservice "harukizmoe/pimoe/internal/application/service"
)

type sessionConfigResponse struct {
	ProviderName    string `json:"provider_name"`
	MaxSteps        int    `json:"max_steps"`
	HasSystemPrompt bool   `json:"has_system_prompt"`
}

type sessionResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Config   sessionConfigResponse `json:"config"`
}

type sessionDetailResponse struct {
	ID        string                   `json:"id"`
	Title     string                   `json:"title"`
	CreatedAt string                   `json:"created_at"`
	UpdatedAt string                   `json:"updated_at"`
	Config   sessionConfigResponse `json:"config"`
	Messages  []sessionMessageResponse `json:"messages"`
}

type sessionMessageResponse struct {
	Role       string                    `json:"role"`
	Content    string                    `json:"content,omitempty"`
	ToolCalls  []sessionToolCallResponse `json:"tool_calls,omitempty"`
	ToolCallID string                    `json:"tool_call_id,omitempty"`
	Tool       string                    `json:"tool,omitempty"`
}

type sessionToolCallResponse struct {
	ID        string          `json:"id"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type sessionsResponse struct {
	Sessions []sessionResponse `json:"sessions"`
}

type runResponse struct {
	Answer string             `json:"answer"`
	Steps  []toolStepResponse `json:"steps"`
}

type toolStepResponse struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    string          `json:"result"`
	Error     string          `json:"error,omitempty"`
}

type providerDiagnosticsResponse struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Model string `json:"model"`
	Ready bool   `json:"ready"`
	Error string `json:"error"`
}

func TestRouterServesHealthAndSessionRoutesThroughGin(t *testing.T) {
	handler := newTestRouter(t)

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	assertStatus(t, health, http.StatusOK)
	if got := decodeJSON[map[string]string](t, health); got["status"] != "ok" {
		t.Fatalf("GET /v1/health = %#v, want status ok", got)
	}

	created := createSession(t, handler, "use calculator to compute 13 * 7")
	assertSession(t, created, "use calculator to compute 13 * 7")

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	assertStatus(t, list, http.StatusOK)
	listed := decodeJSON[sessionsResponse](t, list)
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != created.ID {
		t.Fatalf("GET /v1/sessions = %#v, want created session", listed)
	}

	run := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs", map[string]string{
		"input": "use calculator to compute 13 * 7",
	})
	assertStatus(t, run, http.StatusOK)
	runBody := decodeJSON[runResponse](t, run)
	if runBody.Answer != "13 * 7 = 91" {
		t.Fatalf("run answer = %q, want 13 * 7 = 91", runBody.Answer)
	}
	if len(runBody.Steps) != 1 || runBody.Steps[0].Tool != "calculator" || runBody.Steps[0].Result != "91" {
		t.Fatalf("run steps = %#v, want calculator result", runBody.Steps)
	}
}

func TestRouterReturnsSessionDetailWithStableMessageHistory(t *testing.T) {
	handler := newTestRouter(t)
	created := createSession(t, handler, "use calculator to compute 13 * 7")
	run := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs", map[string]string{
		"input": "use calculator to compute 13 * 7",
	})
	assertStatus(t, run, http.StatusOK)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID, nil))

	assertStatus(t, response, http.StatusOK)
	detail := decodeJSON[sessionDetailResponse](t, response)
	if detail.ID != created.ID || detail.Title != created.Title {
		t.Fatalf("GET /v1/sessions/:id metadata = %#v, want id %q title %q", detail, created.ID, created.Title)
	}
	assertTimestamp(t, "created_at", detail.CreatedAt)
	assertTimestamp(t, "updated_at", detail.UpdatedAt)
	assertCalculatorTranscript(t, detail.Messages)
}

func TestRouterSessionResponsesIncludeConfigSummary(t *testing.T) {
	configPath := writeRouterProvidersConfigContent(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
      timeout_seconds: 1
`)
	handler := newTestRouterWithConfig(t, testRouterConfig{
		root:               filepath.Join(t.TempDir(), "sessions"),
		providerConfigPath: configPath,
		providerName:       "fake-local",
		systemPrompt:       "private system prompt",
		maxSteps:           4,
	})

	created := postJSON(t, handler, "/v1/sessions", map[string]string{"input": "hello", "title": "hello"})
	assertStatus(t, created, http.StatusCreated)
	createdBody := created.Body.String()
	assertBodyContains(t, createdBody, `"config":{"provider_name":"fake-local"`)
	assertBodyContains(t, createdBody, `"max_steps":4`)
	assertBodyContains(t, createdBody, `"has_system_prompt":true`)
	assertBodyNotContains(t, createdBody, "private system prompt")
	assertBodyNotContains(t, createdBody, `"system_prompt":`)
	createdSession := decodeJSONString[sessionResponse](t, createdBody)
	assertConfigSummary(t, createdSession.Config, "fake-local", 4, true)

	listed := getJSON(t, handler, "/v1/sessions")
	assertStatus(t, listed, http.StatusOK)
	listedBody := listed.Body.String()
	assertBodyContains(t, listedBody, `"config":{"provider_name":"fake-local"`)
	assertBodyNotContains(t, listedBody, "private system prompt")
	assertBodyNotContains(t, listedBody, `"system_prompt":`)
	listedSessions := decodeJSONString[sessionsResponse](t, listedBody)
	if len(listedSessions.Sessions) != 1 || listedSessions.Sessions[0].ID != createdSession.ID {
		t.Fatalf("GET /v1/sessions = %#v, want created session %q", listedSessions, createdSession.ID)
	}
	assertConfigSummary(t, listedSessions.Sessions[0].Config, "fake-local", 4, true)

	detail := getJSON(t, handler, "/v1/sessions/"+createdSession.ID)
	assertStatus(t, detail, http.StatusOK)
	detailBody := detail.Body.String()
	assertBodyContains(t, detailBody, `"config":{"provider_name":"fake-local"`)
	assertBodyNotContains(t, detailBody, "private system prompt")
	assertBodyNotContains(t, detailBody, `"system_prompt":`)
	detailSession := decodeJSONString[sessionDetailResponse](t, detailBody)
	assertConfigSummary(t, detailSession.Config, "fake-local", 4, true)
}

func TestRouterCreateAcceptsConfigOverridesWithoutExposingSystemPrompt(t *testing.T) {
	configPath := writeRouterProvidersConfigContent(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
    fake-alt:
      type: fake
      model: fake-tool-model
`)
	handler := newTestRouterWithConfig(t, testRouterConfig{
		root:               filepath.Join(t.TempDir(), "sessions"),
		providerConfigPath: configPath,
		providerName:       "fake-local",
	})

	created := postJSON(t, handler, "/v1/sessions", map[string]any{
		"input":         "hello",
		"provider_name": "fake-alt",
		"max_steps":     5,
		"system_prompt": "private request system prompt",
	})
	assertStatus(t, created, http.StatusCreated)
	createdBody := created.Body.String()
	assertBodyContains(t, createdBody, `"provider_name":"fake-alt"`)
	assertBodyContains(t, createdBody, `"max_steps":5`)
	assertBodyContains(t, createdBody, `"has_system_prompt":true`)
	assertBodyNotContains(t, createdBody, "private request system prompt")
	assertBodyNotContains(t, createdBody, `"system_prompt":`)
	createdSession := decodeJSONString[sessionResponse](t, createdBody)
	assertConfigSummary(t, createdSession.Config, "fake-alt", 5, true)

	listed := getJSON(t, handler, "/v1/sessions")
	assertStatus(t, listed, http.StatusOK)
	listedBody := listed.Body.String()
	assertBodyContains(t, listedBody, `"provider_name":"fake-alt"`)
	assertBodyNotContains(t, listedBody, "private request system prompt")
	assertBodyNotContains(t, listedBody, `"system_prompt":`)
	listedSessions := decodeJSONString[sessionsResponse](t, listedBody)
	if len(listedSessions.Sessions) != 1 || listedSessions.Sessions[0].ID != createdSession.ID {
		t.Fatalf("GET /v1/sessions = %#v, want created session %q", listedSessions, createdSession.ID)
	}
	assertConfigSummary(t, listedSessions.Sessions[0].Config, "fake-alt", 5, true)

	detail := getJSON(t, handler, "/v1/sessions/"+createdSession.ID)
	assertStatus(t, detail, http.StatusOK)
	detailBody := detail.Body.String()
	assertBodyContains(t, detailBody, `"provider_name":"fake-alt"`)
	assertBodyNotContains(t, detailBody, "private request system prompt")
	assertBodyNotContains(t, detailBody, `"system_prompt":`)
	detailSession := decodeJSONString[sessionDetailResponse](t, detailBody)
	assertConfigSummary(t, detailSession.Config, "fake-alt", 5, true)
}

func TestRouterRunAcceptsProviderNameOverride(t *testing.T) {
	handler := newTestRouterWithConfig(t, testRouterConfig{
		root:               filepath.Join(t.TempDir(), "sessions"),
		providerConfigPath: writeRouterSwitchableProvidersConfig(t),
		providerName:       "fake-local",
	})
	created := createSession(t, handler, "switch")

	run := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs", map[string]string{
		"input":         "use calculator to compute 13 * 7",
		"provider_name": "fake-alt",
	})
	assertStatus(t, run, http.StatusOK)

	detail := decodeJSON[sessionDetailResponse](t, getJSON(t, handler, "/v1/sessions/"+created.ID))
	assertConfigSummary(t, detail.Config, "fake-alt", 0, false)
}

func TestRouterStreamAcceptsProviderNameOverride(t *testing.T) {
	handler := newTestRouterWithConfig(t, testRouterConfig{
		root:               filepath.Join(t.TempDir(), "sessions"),
		providerConfigPath: writeRouterSwitchableProvidersConfig(t),
		providerName:       "fake-local",
	})
	created := createSession(t, handler, "stream switch")

	stream := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs/stream", map[string]string{
		"input":         "use calculator to compute 13 * 7",
		"provider_name": "fake-alt",
	})
	assertStatus(t, stream, http.StatusOK)
	assertBodyContains(t, stream.Body.String(), "event:done")

	detail := decodeJSON[sessionDetailResponse](t, getJSON(t, handler, "/v1/sessions/"+created.ID))
	assertConfigSummary(t, detail.Config, "fake-alt", 0, false)
}

func TestRouterRunProviderSelectionErrorReturnsBadRequest(t *testing.T) {
	handler := newTestRouterWithConfig(t, testRouterConfig{
		root:               filepath.Join(t.TempDir(), "sessions"),
		providerConfigPath: writeRouterSwitchableProvidersConfig(t),
		providerName:       "fake-local",
	})
	created := createSession(t, handler, "missing provider")

	run := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs", map[string]string{
		"input":         "use calculator to compute 13 * 7",
		"provider_name": "missing-provider",
	})

	assertStatus(t, run, http.StatusBadRequest)
	body := decodeJSON[errorResponse](t, run)
	if !strings.Contains(body.Error, `unknown provider "missing-provider"`) {
		t.Fatalf("run error = %#v, want unknown provider message", body)
	}
}

func TestRouterStreamProviderSelectionErrorReturnsSSEError(t *testing.T) {
	handler := newTestRouterWithConfig(t, testRouterConfig{
		root:               filepath.Join(t.TempDir(), "sessions"),
		providerConfigPath: writeRouterSwitchableProvidersConfig(t),
		providerName:       "fake-local",
	})
	created := createSession(t, handler, "stream missing provider")

	stream := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs/stream", map[string]string{
		"input":         "use calculator to compute 13 * 7",
		"provider_name": "missing-provider",
	})

	assertStatus(t, stream, http.StatusOK)
	contentType := stream.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	body := stream.Body.String()
	assertBodyContains(t, body, "event:error")
	assertBodyContains(t, body, `unknown provider \"missing-provider\"`)
}

func TestRouterRunsAppendToExistingSessionTranscript(t *testing.T) {
	handler := newTestRouter(t)
	created := createSession(t, handler, "resume calculator")
	// 保证后续 Touch 的 wall-clock 时间跨过 Create 时间,避免 timestamp 断言依赖纳秒级调度。
	time.Sleep(time.Millisecond)

	first := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs", map[string]string{"input": "use calculator to compute 13 * 7"})
	assertStatus(t, first, http.StatusOK)
	firstBody := decodeJSON[runResponse](t, first)
	if firstBody.Answer != "13 * 7 = 91" {
		t.Fatalf("first answer = %q, want 13 * 7 = 91", firstBody.Answer)
	}

	second := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs", map[string]string{"input": "what was the previous result?"})
	assertStatus(t, second, http.StatusOK)
	secondBody := decodeJSON[runResponse](t, second)
	if secondBody.Answer != "previous result was 91" {
		t.Fatalf("second answer = %q, want previous result was 91", secondBody.Answer)
	}

	detailResp := httptest.NewRecorder()
	handler.ServeHTTP(detailResp, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+created.ID, nil))
	assertStatus(t, detailResp, http.StatusOK)
	detail := decodeJSON[sessionDetailResponse](t, detailResp)
	if len(detail.Messages) != 6 {
		t.Fatalf("detail messages len = %d, want 6: %#v", len(detail.Messages), detail.Messages)
	}
	assertCalculatorTranscript(t, detail.Messages[:4])
	if detail.Messages[4].Role != "user" || detail.Messages[4].Content != "what was the previous result?" {
		t.Fatalf("second user message = %#v", detail.Messages[4])
	}
	if detail.Messages[5].Role != "assistant" || detail.Messages[5].Content != "previous result was 91" {
		t.Fatalf("second assistant message = %#v", detail.Messages[5])
	}
	createdUpdated, err := time.Parse(time.RFC3339Nano, created.UpdatedAt)
	if err != nil {
		t.Fatalf("created updated_at = %q, want RFC3339Nano: %v", created.UpdatedAt, err)
	}
	detailUpdated, err := time.Parse(time.RFC3339Nano, detail.UpdatedAt)
	if err != nil {
		t.Fatalf("detail updated_at = %q, want RFC3339Nano: %v", detail.UpdatedAt, err)
	}
	if !detailUpdated.After(createdUpdated) {
		t.Fatalf("updated_at = %s, want after %s", detail.UpdatedAt, created.UpdatedAt)
	}
}

func TestRouterGetMissingSessionReturnsJSON404(t *testing.T) {
	handler := newTestRouter(t)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/sessions/missing", nil))

	assertStatus(t, response, http.StatusNotFound)
	body := decodeJSON[errorResponse](t, response)
	if body.Error != `session "missing" not found` {
		t.Fatalf("GET /v1/sessions/missing error = %#v, want exact missing session message", body)
	}
}

func TestRouterRunMissingSessionReturnsJSON404(t *testing.T) {
	handler := newTestRouter(t)

	response := postJSON(t, handler, "/v1/sessions/missing/runs", map[string]string{"input": "use calculator to compute 13 * 7"})

	assertStatus(t, response, http.StatusNotFound)
	body := decodeJSON[errorResponse](t, response)
	if body.Error != `session "missing" not found` {
		t.Fatalf("POST /v1/sessions/missing/runs error = %#v, want exact missing session message", body)
	}
}

func TestRouterStreamsSessionRunAsSSE(t *testing.T) {
	handler := newTestRouter(t)
	created := createSession(t, handler, "stream calculator")

	response := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs/stream", map[string]string{
		"input": "use calculator to compute 13 * 7",
	})

	assertStatus(t, response, http.StatusOK)
	contentType := response.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	body := response.Body.String()
	for _, want := range []string{
		"event:delta\n",
		`data:{"content":"13 * 7 = 91"}`,
		"event:tool_call\n",
		`"tool":"calculator"`,
		"event:tool_result\n",
		`"result":"91"`,
		"event:done\n",
		`"answer":"13 * 7 = 91"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q in:\n%s", want, body)
		}
	}
}

func TestRouterStreamsValidationErrorsAsSSE(t *testing.T) {
	handler := newTestRouter(t)
	created := createSession(t, handler, "stream validation")

	response := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs/stream", map[string]string{
		"input": "",
	})

	assertStatus(t, response, http.StatusOK)
	contentType := response.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}
	body := response.Body.String()
	for _, want := range []string{
		"event:error",
		`data:{"error":"input must not be empty"}`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q in:\n%s", want, body)
		}
	}
}

func TestRouterReturnsCurrentProviderDiagnostics(t *testing.T) {
	handler := newTestRouter(t)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/providers/current", nil))

	assertStatus(t, response, http.StatusOK)
	got := decodeJSON[providerDiagnosticsResponse](t, response)
	want := providerDiagnosticsResponse{
		Name:  "fake",
		Type:  "fake",
		Model: "fake-tool-model",
		Ready: true,
		Error: "",
	}
	if got != want {
		t.Fatalf("GET /v1/providers/current = %#v, want %#v", got, want)
	}
}

type testRouterConfig struct {
	root               string
	providerConfigPath string
	providerName       string
	systemPrompt       string
	maxSteps           int
}

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	return newTestRouterWithConfig(t, testRouterConfig{
		root:               filepath.Join(t.TempDir(), "sessions"),
		providerConfigPath: writeRouterProvidersConfig(t),
		providerName:       "fake",
	})
}

func newTestRouterWithConfig(t *testing.T, cfg testRouterConfig) http.Handler {
	t.Helper()
	if cfg.root == "" {
		cfg.root = filepath.Join(t.TempDir(), "sessions")
	}
	if cfg.providerConfigPath == "" {
		cfg.providerConfigPath = writeRouterProvidersConfig(t)
	}
	if cfg.providerName == "" {
		cfg.providerName = "fake"
	}
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              appdata.NewManagerSessionStore(cfg.root),
		ProviderConfigPath: cfg.providerConfigPath,
		ProviderName:       cfg.providerName,
		SystemPrompt:       cfg.systemPrompt,
		MaxSteps:           cfg.maxSteps,
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	return approuter.New(approuter.Config{SessionService: svc})
}

func createSession(t *testing.T, handler http.Handler, input string) sessionResponse {
	t.Helper()
	response := postJSON(t, handler, "/v1/sessions", map[string]string{"input": input})
	assertStatus(t, response, http.StatusCreated)
	return decodeJSON[sessionResponse](t, response)
}

func postJSON(t *testing.T, handler http.Handler, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(body); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &payload)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func getJSON(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeJSON[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var got T
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode JSON error = %v; body = %q", err, response.Body.String())
	}
	return got
}

func decodeJSONString[T any](t *testing.T, body string) T {
	t.Helper()
	var got T
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode JSON error = %v; body = %q", err, body)
	}
	return got
}

func assertStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, want, response.Body.String())
	}
}

func assertSession(t *testing.T, got sessionResponse, wantTitle string) {
	t.Helper()
	if got.ID == "" || got.Title != wantTitle {
		t.Fatalf("session = %#v, want id and title %q", got, wantTitle)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.CreatedAt); err != nil {
		t.Fatalf("created_at = %q, want RFC3339Nano: %v", got.CreatedAt, err)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.UpdatedAt); err != nil {
		t.Fatalf("updated_at = %q, want RFC3339Nano: %v", got.UpdatedAt, err)
	}
}

func assertConfigSummary(t *testing.T, got sessionConfigResponse, wantProvider string, wantMaxSteps int, wantHasSystemPrompt bool) {
	t.Helper()
	if got.ProviderName != wantProvider || got.MaxSteps != wantMaxSteps || got.HasSystemPrompt != wantHasSystemPrompt {
		t.Fatalf("config = %#v, want provider %q max_steps %d has_system_prompt %t", got, wantProvider, wantMaxSteps, wantHasSystemPrompt)
	}
}

func assertBodyContains(t *testing.T, body string, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("body missing %q in:\n%s", want, body)
	}
}

func assertBodyNotContains(t *testing.T, body string, unwanted string) {
	t.Helper()
	if strings.Contains(body, unwanted) {
		t.Fatalf("body contains private value %q in:\n%s", unwanted, body)
	}
}

func assertTimestamp(t *testing.T, field string, value string) {
	t.Helper()
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		t.Fatalf("%s = %q, want RFC3339Nano: %v", field, value, err)
	}
}

func assertCalculatorTranscript(t *testing.T, messages []sessionMessageResponse) {
	t.Helper()
	if len(messages) != 4 {
		t.Fatalf("messages len = %d, want 4 terminal messages: %#v", len(messages), messages)
	}
	user := messages[0]
	if user.Role != "user" || user.Content != "use calculator to compute 13 * 7" {
		t.Fatalf("user message = %#v, want calculator prompt", user)
	}
	assistantToolCall := messages[1]
	if assistantToolCall.Role != "assistant" || len(assistantToolCall.ToolCalls) != 1 {
		t.Fatalf("assistant tool call message = %#v, want one calculator tool call", assistantToolCall)
	}
	call := assistantToolCall.ToolCalls[0]
	if call.ID != "call_fake_calculator" || call.Tool != "calculator" {
		t.Fatalf("tool call = %#v, want calculator call", call)
	}
	assertRawJSONEqual(t, call.Arguments, `{"a":13,"b":7,"op":"mul"}`)
	toolResult := messages[2]
	if toolResult.Role != "tool" || toolResult.ToolCallID != call.ID || toolResult.Tool != "calculator" || toolResult.Content != "91" {
		t.Fatalf("tool result message = %#v, want calculator result 91 for call %q", toolResult, call.ID)
	}
	finalAssistant := messages[3]
	if finalAssistant.Role != "assistant" || finalAssistant.Content != "13 * 7 = 91" || len(finalAssistant.ToolCalls) != 0 {
		t.Fatalf("final assistant message = %#v, want final answer", finalAssistant)
	}
}

func assertRawJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	if !json.Valid(got) {
		t.Fatalf("JSON value = %q, want valid JSON", string(got))
	}
	var normalizedGot any
	if err := json.Unmarshal(got, &normalizedGot); err != nil {
		t.Fatalf("unmarshal JSON value %q: %v", string(got), err)
	}
	var normalizedWant any
	if err := json.Unmarshal([]byte(want), &normalizedWant); err != nil {
		t.Fatalf("unmarshal expected JSON %q: %v", want, err)
	}
	if !reflect.DeepEqual(normalizedGot, normalizedWant) {
		t.Fatalf("JSON value = %#v, want %#v", normalizedGot, normalizedWant)
	}
}

func writeRouterProvidersConfig(t *testing.T) string {
	t.Helper()
	return writeRouterProvidersConfigContent(t, `llms:
  default_provider: fake
  providers:
    fake:
      type: fake
      model: fake-tool-model
      timeout_seconds: 1
`)
}

func writeRouterSwitchableProvidersConfig(t *testing.T) string {
	t.Helper()
	return writeRouterProvidersConfigContent(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
      timeout_seconds: 1
    fake-alt:
      type: fake
      model: fake-tool-model
      timeout_seconds: 1
`)
}

func writeRouterProvidersConfigContent(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "providers.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write providers config: %v", err)
	}
	return path
}
