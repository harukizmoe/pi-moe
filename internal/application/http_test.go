package application_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	approuter "harukizmoe/pimoe/internal/application/router"
	appservice "harukizmoe/pimoe/internal/application/service"
)

type httpSessionResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type listSessionsResponse struct {
	Sessions []httpSessionResponse `json:"sessions"`
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

func TestHTTPHealthReturnsOK(t *testing.T) {
	handler := newTestHTTPHandler(t)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

	assertHTTPStatus(t, response, http.StatusOK)
	got := decodeJSON[map[string]string](t, response)
	want := map[string]string{"status": "ok"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GET /v1/health response = %#v, want %#v", got, want)
	}
}

func TestHTTPCreateSessionReturnsSessionMetadata(t *testing.T) {
	handler := newTestHTTPHandler(t)

	got := createSessionOverHTTP(t, handler, "use calculator to compute 13 * 7")

	assertHTTPSessionMetadata(t, got, "use calculator to compute 13 * 7")
}

func TestHTTPListSessionsReturnsCreatedSessions(t *testing.T) {
	handler := newTestHTTPHandler(t)
	created := createSessionOverHTTP(t, handler, "use calculator to compute 13 * 7")

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))

	assertHTTPStatus(t, response, http.StatusOK)
	got := decodeJSON[listSessionsResponse](t, response)
	if len(got.Sessions) != 1 {
		t.Fatalf("GET /v1/sessions returned %d sessions, want 1: %#v", len(got.Sessions), got.Sessions)
	}
	assertHTTPSessionMetadata(t, got.Sessions[0], created.Title)
	if got.Sessions[0].ID != created.ID {
		t.Fatalf("GET /v1/sessions session id = %q, want created id %q", got.Sessions[0].ID, created.ID)
	}
}

func TestHTTPRunSessionReturnsAnswerAndToolSteps(t *testing.T) {
	handler := newTestHTTPHandler(t)
	created := createSessionOverHTTP(t, handler, "use calculator to compute 13 * 7")

	response := postJSON(t, handler, "/v1/sessions/"+created.ID+"/runs", map[string]string{
		"input": "use calculator to compute 13 * 7",
	})

	assertHTTPStatus(t, response, http.StatusOK)
	got := decodeJSON[runResponse](t, response)
	if got.Answer != "13 * 7 = 91" {
		t.Fatalf("POST /v1/sessions/{id}/runs answer = %q, want %q", got.Answer, "13 * 7 = 91")
	}
	if len(got.Steps) != 1 {
		t.Fatalf("POST /v1/sessions/{id}/runs steps len = %d, want 1: %#v", len(got.Steps), got.Steps)
	}
	step := got.Steps[0]
	if step.Tool != "calculator" {
		t.Fatalf("tool step tool = %q, want calculator", step.Tool)
	}
	if step.Result != "91" {
		t.Fatalf("tool step result = %q, want 91", step.Result)
	}
	if step.Error != "" {
		t.Fatalf("tool step error = %q, want empty", step.Error)
	}
	var arguments struct {
		A  float64 `json:"a"`
		B  float64 `json:"b"`
		Op string  `json:"op"`
	}
	if err := json.Unmarshal(step.Arguments, &arguments); err != nil {
		t.Fatalf("tool step arguments decode error = %v; raw = %s", err, step.Arguments)
	}
	if arguments.A != 13 || arguments.B != 7 || arguments.Op != "mul" {
		t.Fatalf("tool step arguments = %#v, want a=13 b=7 op=mul", arguments)
	}
}

func newTestHTTPHandler(t *testing.T) http.Handler {
	t.Helper()

	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		SessionRoot:        filepath.Join(t.TempDir(), "sessions"),
		ProviderConfigPath: writeHTTPProvidersConfig(t),
		ProviderName:       "fake",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	return approuter.New(approuter.Config{SessionService: svc})
}

func createSessionOverHTTP(t *testing.T, handler http.Handler, input string) httpSessionResponse {
	t.Helper()

	response := postJSON(t, handler, "/v1/sessions", map[string]string{"input": input})
	assertHTTPStatus(t, response, http.StatusCreated)
	return decodeJSON[httpSessionResponse](t, response)
}

func postJSON(t *testing.T, handler http.Handler, target string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(body); err != nil {
		t.Fatalf("encode request body: %v", err)
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
		t.Fatalf("decode response JSON error = %v; body = %q", err, response.Body.String())
	}
	return got
}

func assertHTTPStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()

	if response.Code != want {
		t.Fatalf("HTTP status = %d, want %d; body = %q", response.Code, want, response.Body.String())
	}
}

func assertHTTPSessionMetadata(t *testing.T, got httpSessionResponse, wantTitle string) {
	t.Helper()

	if got.ID == "" {
		t.Fatal("session id is empty")
	}
	if got.Title != wantTitle {
		t.Fatalf("session title = %q, want %q", got.Title, wantTitle)
	}
	createdAt := parseHTTPTime(t, "created_at", got.CreatedAt)
	updatedAt := parseHTTPTime(t, "updated_at", got.UpdatedAt)
	if updatedAt.Before(createdAt) {
		t.Fatalf("updated_at %s is before created_at %s", got.UpdatedAt, got.CreatedAt)
	}
}

func parseHTTPTime(t *testing.T, field string, value string) time.Time {
	t.Helper()

	if value == "" {
		t.Fatalf("%s is empty", field)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("%s = %q, want RFC3339 timestamp: %v", field, value, err)
	}
	if parsed.IsZero() {
		t.Fatalf("%s parsed as zero time", field)
	}
	return parsed
}

func writeHTTPProvidersConfig(t *testing.T) string {
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
