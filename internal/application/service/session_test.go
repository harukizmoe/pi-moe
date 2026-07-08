package service_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appdata "harukizmoe/pimoe/internal/application/data"
	appservice "harukizmoe/pimoe/internal/application/service"
)

func TestSessionServiceCreateListAndRunUsesStoreBoundary(t *testing.T) {
	ctx := context.Background()
	sessionRoot := filepath.Join(t.TempDir(), "sessions")
	store := appdata.NewManagerSessionStore(sessionRoot)
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: writeProvidersConfig(t),
		ProviderName:       "fake-local",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	created, err := svc.Create(ctx, "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create() ID is empty")
	}
	if created.Title != "use calculator to compute 13 * 7" {
		t.Fatalf("Create() Title = %q, want prompt-derived title", created.Title)
	}
	if filepath.Dir(created.Path) != sessionRoot {
		t.Fatalf("Create() Path dir = %q, want %q", filepath.Dir(created.Path), sessionRoot)
	}

	listed, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("List() = %#v, want created session", listed)
	}

	result, err := svc.Run(ctx, created.ID, "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Answer != "13 * 7 = 91" {
		t.Fatalf("Run() Answer = %q, want %q", result.Answer, "13 * 7 = 91")
	}
	if len(result.ToolSteps) != 1 {
		t.Fatalf("Run() ToolSteps len = %d, want 1: %#v", len(result.ToolSteps), result.ToolSteps)
	}
	step := result.ToolSteps[0]
	if step.ToolName != "calculator" || step.Arguments != `{"a":13,"b":7,"op":"mul"}` || step.Result != "91" {
		t.Fatalf("Run() ToolSteps[0] = %#v, want calculator args/result", step)
	}
}

func TestSessionServiceGetReturnsMetadataAndTerminalMessages(t *testing.T) {
	ctx := context.Background()
	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: writeProvidersConfig(t),
		ProviderName:       "fake-local",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	created, err := svc.Create(ctx, "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := svc.Run(ctx, created.ID, "use calculator to compute 13 * 7"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantMetas, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(wantMetas) != 1 {
		t.Fatalf("List() len = %d, want 1: %#v", len(wantMetas), wantMetas)
	}

	detail, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	wantMeta := wantMetas[0]
	if detail.ID != wantMeta.ID || detail.Title != wantMeta.Title || !detail.CreatedAt.Equal(wantMeta.CreatedAt) || !detail.UpdatedAt.Equal(wantMeta.UpdatedAt) {
		t.Fatalf("Get() metadata = %#v, want %#v", detail, wantMeta)
	}
	if len(detail.Messages) != 4 {
		t.Fatalf("Get() Messages len = %d, want 4 terminal messages: %#v", len(detail.Messages), detail.Messages)
	}

	user := detail.Messages[0]
	if user.Role != "user" || user.Content != "use calculator to compute 13 * 7" {
		t.Fatalf("Get() user message = %#v, want calculator prompt", user)
	}
	assistantToolCall := detail.Messages[1]
	if assistantToolCall.Role != "assistant" || len(assistantToolCall.ToolCalls) != 1 {
		t.Fatalf("Get() assistant tool call message = %#v, want one calculator tool call", assistantToolCall)
	}
	call := assistantToolCall.ToolCalls[0]
	if call.ID != "call_fake_calculator" || call.Tool != "calculator" {
		t.Fatalf("Get() tool call = %#v, want calculator call", call)
	}
	assertJSONEqual(t, call.Arguments, `{"a":13,"b":7,"op":"mul"}`)

	toolResult := detail.Messages[2]
	if toolResult.Role != "tool" || toolResult.ToolCallID != call.ID || toolResult.Tool != "calculator" || toolResult.Content != "91" {
		t.Fatalf("Get() tool result message = %#v, want calculator result 91 for call %q", toolResult, call.ID)
	}
	finalAssistant := detail.Messages[3]
	if finalAssistant.Role != "assistant" || finalAssistant.Content != "13 * 7 = 91" || len(finalAssistant.ToolCalls) != 0 {
		t.Fatalf("Get() final assistant message = %#v, want final answer", finalAssistant)
	}
}

func TestSessionServiceGetDoesNotRequireProviderConfigToLoadTranscript(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "sessions")
	store := appdata.NewManagerSessionStore(root)
	workingConfig := writeProvidersConfig(t)
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: workingConfig,
		ProviderName:       "fake-local",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	created, err := svc.Create(ctx, "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := svc.Run(ctx, created.ID, "use calculator to compute 13 * 7"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	brokenConfig := writeProvidersConfigContent(t, `llms:
  default_provider: broken
  providers:
    broken:
      type: openai_compatible
      model: gpt-test
`)
	readOnlySvc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: brokenConfig,
		ProviderName:       "broken",
	})
	if err != nil {
		t.Fatalf("NewSessionService() with broken provider config error = %v", err)
	}

	detail, err := readOnlySvc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() with broken provider config error = %v", err)
	}
	if len(detail.Messages) != 4 || detail.Messages[3].Content != "13 * 7 = 91" {
		t.Fatalf("Get() Messages = %#v, want persisted calculator transcript", detail.Messages)
	}
}

func TestSessionServiceGetReturnsMalformedToolArgumentsAsJSONString(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "sessions")
	store := appdata.NewManagerSessionStore(root)
	created, err := store.Create(ctx, "malformed tool args")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeSessionJSONL(t, created.Path, []string{
		`{"id":"m1","type":"message","timestamp":"2026-07-08T00:00:00Z","message":{"role":"assistant","tool_calls":[{"id":"bad-call","type":"function","function":{"name":"calculator","arguments":"{bad json"}}]}}`,
		`{"id":"m2","parent_id":"m1","type":"message","timestamp":"2026-07-08T00:00:01Z","message":{"role":"tool","tool_call_id":"bad-call","tool_name":"calculator","content":"invalid arguments","is_error":true}}`,
		`{"id":"leaf","parent_id":"m2","type":"leaf","timestamp":"2026-07-08T00:00:02Z","leaf":{"entry_id":"m2"}}`,
	})
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: writeProvidersConfig(t),
		ProviderName:       "fake-local",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	detail, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	payload, err := json.Marshal(detail.Messages)
	if err != nil {
		t.Fatalf("marshal Get() messages error = %v; messages = %#v", err, detail.Messages)
	}
	if !strings.Contains(string(payload), `"Arguments":"{bad json"`) {
		t.Fatalf("marshaled messages = %s, want malformed arguments preserved as JSON string", payload)
	}
}

func writeProvidersConfig(t *testing.T) string {
	t.Helper()
	return writeProvidersConfigContent(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
}

func writeProvidersConfigContent(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "providers.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write providers config: %v", err)
	}
	return path
}

func writeSessionJSONL(t *testing.T, path string, lines []string) {
	t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write session JSONL: %v", err)
	}
}

func TestSessionServiceStreamReturnsStableApplicationEvents(t *testing.T) {
	ctx := context.Background()
	sessionRoot := filepath.Join(t.TempDir(), "sessions")
	store := appdata.NewManagerSessionStore(sessionRoot)
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: writeProvidersConfig(t),
		ProviderName:       "fake-local",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	created, err := svc.Create(ctx, "stream calculator")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	events, err := svc.Stream(ctx, created.ID, "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	got := collectStreamEvents(t, events)

	assertHasStreamEvent(t, got, "delta", map[string]any{"content": "13 * 7 = 91"})
	assertHasStreamEvent(t, got, "tool_call", map[string]any{
		"id":        "call_fake_calculator",
		"tool":      "calculator",
		"arguments": map[string]any{"a": float64(13), "b": float64(7), "op": "mul"},
	})
	assertHasStreamEvent(t, got, "tool_result", map[string]any{
		"id":     "call_fake_calculator",
		"tool":   "calculator",
		"result": "91",
	})
	assertHasStreamEvent(t, got, "done", map[string]any{"answer": "13 * 7 = 91"})
}

func TestSessionServiceCurrentProviderDiagnosticsReportsFakeProviderReady(t *testing.T) {
	ctx := context.Background()
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions")),
		ProviderConfigPath: writeProvidersConfig(t),
		ProviderName:       "fake-local",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	got, err := svc.CurrentProviderDiagnostics(ctx)
	if err != nil {
		t.Fatalf("CurrentProviderDiagnostics() error = %v", err)
	}
	want := appservice.ProviderDiagnostics{
		Name:  "fake-local",
		Type:  "fake",
		Model: "fake-tool-model",
		Ready: true,
		Error: "",
	}
	if got != want {
		t.Fatalf("CurrentProviderDiagnostics() = %#v, want %#v", got, want)
	}
}

func TestSessionServiceCurrentProviderDiagnosticsReportsMissingAPIKeyEnvWithoutSecret(t *testing.T) {
	ctx := context.Background()
	unsetEnvForTest(t, "TEST_PROVIDER_KEY")
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store: appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions")),
		ProviderConfigPath: writeProvidersConfigContent(t, `llms:
  default_provider: test-openai
  providers:
    test-openai:
      type: openai_compatible
      base_url: "https://example.invalid/v1"
      api_key_env: TEST_PROVIDER_KEY
      model: gpt-test
`),
		ProviderName: "test-openai",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	got, err := svc.CurrentProviderDiagnostics(ctx)
	if err != nil {
		t.Fatalf("CurrentProviderDiagnostics() error = %v", err)
	}
	want := appservice.ProviderDiagnostics{
		Name:  "test-openai",
		Type:  "openai_compatible",
		Model: "gpt-test",
		Ready: false,
		Error: "environment variable TEST_PROVIDER_KEY is not set",
	}
	if got != want {
		t.Fatalf("CurrentProviderDiagnostics() = %#v, want %#v", got, want)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal diagnostics: %v", err)
	}
	if strings.Contains(string(payload), "api_key") || strings.Contains(string(payload), "secret") {
		t.Fatalf("diagnostics payload leaks key material: %s", payload)
	}
}

func TestSessionServiceCurrentProviderDiagnosticsReportsMissingBaseURL(t *testing.T) {
	ctx := context.Background()
	t.Setenv("TEST_PROVIDER_KEY", "test-secret-value")
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store: appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions")),
		ProviderConfigPath: writeProvidersConfigContent(t, `llms:
  default_provider: test-openai
  providers:
    test-openai:
      type: openai_compatible
      api_key_env: TEST_PROVIDER_KEY
      model: gpt-test
`),
		ProviderName: "test-openai",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	got, err := svc.CurrentProviderDiagnostics(ctx)
	if err != nil {
		t.Fatalf("CurrentProviderDiagnostics() error = %v", err)
	}
	want := appservice.ProviderDiagnostics{
		Name:  "test-openai",
		Type:  "openai_compatible",
		Model: "gpt-test",
		Ready: false,
		Error: "openai-compatible base_url is required",
	}
	if got != want {
		t.Fatalf("CurrentProviderDiagnostics() = %#v, want %#v", got, want)
	}
}

func TestSessionServiceCurrentProviderDiagnosticsReportsUnknownProviderType(t *testing.T) {
	ctx := context.Background()
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store: appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions")),
		ProviderConfigPath: writeProvidersConfigContent(t, `llms:
  default_provider: mystery
  providers:
    mystery:
      type: unsupported
      model: mystery-model
`),
		ProviderName: "mystery",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	got, err := svc.CurrentProviderDiagnostics(ctx)
	if err != nil {
		t.Fatalf("CurrentProviderDiagnostics() error = %v", err)
	}
	want := appservice.ProviderDiagnostics{
		Name:  "mystery",
		Type:  "unsupported",
		Model: "mystery-model",
		Ready: false,
		Error: `unknown llm provider type "unsupported"`,
	}
	if got != want {
		t.Fatalf("CurrentProviderDiagnostics() = %#v, want %#v", got, want)
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			if err := os.Setenv(key, old); err != nil {
				t.Fatalf("restore %s: %v", key, err)
			}
			return
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("restore unset %s: %v", key, err)
		}
	})
}

func collectStreamEvents(t *testing.T, events <-chan appservice.StreamEvent) []appservice.StreamEvent {
	t.Helper()
	var got []appservice.StreamEvent
	for event := range events {
		got = append(got, event)
	}
	return got
}

func assertHasStreamEvent(t *testing.T, events []appservice.StreamEvent, name string, data map[string]any) {
	t.Helper()
	for _, event := range events {
		if event.Name != name {
			continue
		}
		got := normalizeStreamData(t, event.Data)
		if reflect.DeepEqual(got, data) {
			return
		}
	}
	t.Fatalf("missing stream event %q with data %#v in %#v", name, data, events)
}

func normalizeStreamData(t *testing.T, value any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal stream data: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatalf("unmarshal stream data: %v", err)
	}
	return out
}

func assertJSONEqual(t *testing.T, got any, want string) {
	t.Helper()
	var gotBytes []byte
	switch value := got.(type) {
	case json.RawMessage:
		gotBytes = value
	case []byte:
		gotBytes = value
	case string:
		gotBytes = []byte(value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal JSON value %T: %v", got, err)
		}
		gotBytes = encoded
	}
	if !json.Valid(gotBytes) {
		t.Fatalf("JSON value = %q, want valid JSON", string(gotBytes))
	}
	var normalizedGot any
	if err := json.Unmarshal(gotBytes, &normalizedGot); err != nil {
		t.Fatalf("unmarshal JSON value %q: %v", string(gotBytes), err)
	}
	var normalizedWant any
	if err := json.Unmarshal([]byte(want), &normalizedWant); err != nil {
		t.Fatalf("unmarshal expected JSON %q: %v", want, err)
	}
	if !reflect.DeepEqual(normalizedGot, normalizedWant) {
		t.Fatalf("JSON value = %#v, want %#v", normalizedGot, normalizedWant)
	}
}
