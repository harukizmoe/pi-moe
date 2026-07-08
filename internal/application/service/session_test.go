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
