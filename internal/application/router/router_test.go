package router_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appdata "harukizmoe/pimoe/internal/application/data"
	approuter "harukizmoe/pimoe/internal/application/router"
	appservice "harukizmoe/pimoe/internal/application/service"
)

type sessionResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
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

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions")),
		ProviderConfigPath: writeRouterProvidersConfig(t),
		ProviderName:       "fake",
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

func decodeJSON[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var got T
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatalf("decode JSON error = %v; body = %q", err, response.Body.String())
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

func writeRouterProvidersConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "providers.yaml")
	content := `llms:
  default_provider: fake
  providers:
    fake:
      type: fake
      model: fake-tool-model
      timeout_seconds: 1
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write providers config: %v", err)
	}
	return path
}
