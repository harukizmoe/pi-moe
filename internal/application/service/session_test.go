package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	appdata "harukizmoe/pimoe/internal/application/data"
	appservice "harukizmoe/pimoe/internal/application/service"
	"harukizmoe/pimoe/internal/session"
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

func TestSessionServiceCreateStoresConfigSummary(t *testing.T) {
	ctx := context.Background()
	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: writeProvidersConfig(t),
		ProviderName:       "fake-local",
		MaxSteps:           3,
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	created, err := svc.Create(ctx, "prompt")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Config.ProviderName != "fake-local" || created.Config.MaxSteps != 3 {
		t.Fatalf("Create() Config = %#v, want provider fake-local and max_steps 3", created.Config)
	}
}

func TestSessionServiceRunCombinesBaseAndSessionPromptsForProviderRequestWithoutPersistingPrompts(t *testing.T) {
	ctx := context.Background()
	basePrompt := "project base prompt"
	sessionPrompt := "private managed run session prompt"
	server, requests := newCapturingOpenAICompatibleServer(t)
	defer server.Close()

	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: writeOpenAICompatibleProvidersConfig(t, server.URL+"/v1"),
		ProviderName:       "test-openai",
		BaseSystemPrompt:   basePrompt,
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	created, err := svc.Create(ctx, "managed run", appservice.CreateOptions{SessionPrompt: sessionPrompt})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Config.SessionPrompt != sessionPrompt {
		t.Fatalf("created SessionPrompt = %q, want stored session prompt", created.Config.SessionPrompt)
	}

	result, err := svc.Run(ctx, created.ID, "hello from managed run")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Answer != "managed answer" {
		t.Fatalf("Run() Answer = %q, want managed answer", result.Answer)
	}
	captured := receiveOpenAIRequest(t, requests)
	want := basePrompt + "\n\nSession prompt:\n" + sessionPrompt
	assertProviderReceivedSystemMessageBeforeUser(t, captured.Messages, want, "hello from managed run")

	detail, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.Config.SessionPrompt != sessionPrompt {
		t.Fatalf("detail SessionPrompt = %q, want stored session prompt", detail.Config.SessionPrompt)
	}
	assertTranscriptDoesNotIncludeProviderPrompt(t, detail.Messages, basePrompt, "hello from managed run")
	assertTranscriptDoesNotIncludeProviderPrompt(t, detail.Messages, sessionPrompt, "hello from managed run")
}

func TestSessionServiceRunAppliesConfiguredBaseSystemPromptWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	basePrompt := "private service base prompt"
	server, requests := newCapturingOpenAICompatibleServer(t)
	defer server.Close()

	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: writeOpenAICompatibleProvidersConfig(t, server.URL+"/v1"),
		ProviderName:       "test-openai",
		BaseSystemPrompt:   basePrompt,
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	created, err := svc.Create(ctx, "managed base")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Config.SessionPrompt != "" {
		t.Fatalf("created SessionPrompt = %q, want no stored session prompt", created.Config.SessionPrompt)
	}

	if _, err := svc.Run(ctx, created.ID, "hello from managed base"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	captured := receiveOpenAIRequest(t, requests)
	assertProviderReceivedSystemMessageBeforeUser(t, captured.Messages, basePrompt, "hello from managed base")

	detail, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.Config.SessionPrompt != "" {
		t.Fatalf("detail SessionPrompt = %q, want no stored session prompt", detail.Config.SessionPrompt)
	}
	assertTranscriptDoesNotIncludeProviderPrompt(t, detail.Messages, basePrompt, "hello from managed base")
}

func TestSessionServiceStreamCombinesBaseAndSessionPromptsForProviderRequestWithoutPersistingPrompts(t *testing.T) {
	ctx := context.Background()
	basePrompt := "project base prompt"
	sessionPrompt := "private managed stream session prompt"
	server, requests := newCapturingOpenAICompatibleServer(t)
	defer server.Close()

	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: writeOpenAICompatibleProvidersConfig(t, server.URL+"/v1"),
		ProviderName:       "test-openai",
		BaseSystemPrompt:   basePrompt,
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	created, err := svc.Create(ctx, "managed stream", appservice.CreateOptions{SessionPrompt: sessionPrompt})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Config.SessionPrompt != sessionPrompt {
		t.Fatalf("created SessionPrompt = %q, want stored session prompt", created.Config.SessionPrompt)
	}

	events, err := svc.Stream(ctx, created.ID, "hello from managed stream")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	got := collectStreamEvents(t, events)
	assertHasStreamEvent(t, got, "done", map[string]any{"answer": "managed answer"})
	captured := receiveOpenAIRequest(t, requests)
	want := basePrompt + "\n\nSession prompt:\n" + sessionPrompt
	assertProviderReceivedSystemMessageBeforeUser(t, captured.Messages, want, "hello from managed stream")

	detail, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.Config.SessionPrompt != sessionPrompt {
		t.Fatalf("detail SessionPrompt = %q, want stored session prompt", detail.Config.SessionPrompt)
	}
	assertTranscriptDoesNotIncludeProviderPrompt(t, detail.Messages, basePrompt, "hello from managed stream")
	assertTranscriptDoesNotIncludeProviderPrompt(t, detail.Messages, sessionPrompt, "hello from managed stream")
}

func TestSessionServiceCreatePinsResolvedDefaultProvider(t *testing.T) {
	ctx := context.Background()
	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	configPath := writeProvidersConfigContent(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
    fake-alt:
      type: openai_compatible
      base_url: "https://example.invalid/v1"
      api_key_env: TEST_PROVIDER_KEY
      model: broken-default-model
`)
	svc, err := appservice.NewSessionService(appservice.SessionConfig{Store: store, ProviderConfigPath: configPath})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	created, err := svc.Create(ctx, "pin default provider")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Config.ProviderName != "fake-local" {
		t.Fatalf("Create() ProviderName = %q, want resolved default fake-local", created.Config.ProviderName)
	}

	if err := os.WriteFile(configPath, []byte(`llms:
  default_provider: fake-alt
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
    fake-alt:
      type: openai_compatible
      base_url: "https://example.invalid/v1"
      api_key_env: TEST_PROVIDER_KEY
      model: broken-default-model
`), 0o600); err != nil {
		t.Fatalf("rewrite providers config: %v", err)
	}
	result, err := svc.Run(ctx, created.ID, "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Run() after default_provider change error = %v", err)
	}
	if result.Answer != "13 * 7 = 91" {
		t.Fatalf("Run() Answer = %q, want pinned fake provider answer", result.Answer)
	}
}

func TestSessionServiceRunAllowsExplicitProviderOverrideAndPersistsAfterSuccess(t *testing.T) {
	ctx := context.Background()
	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	configPath := writeProvidersConfigContent(t, `llms:
  default_provider: missing-default
  providers:
    fake-old:
      type: openai_compatible
      base_url: "https://example.invalid/v1"
      api_key_env: TEST_PROVIDER_KEY
      model: bad
    fake-new:
      type: fake
      model: fake-tool-model
`)
	svc, err := appservice.NewSessionService(appservice.SessionConfig{Store: store, ProviderConfigPath: configPath, ProviderName: "fake-old"})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	created, err := svc.Create(ctx, "switch provider")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := svc.Run(ctx, created.ID, "use calculator to compute 13 * 7", appservice.RunOptions{ProviderName: "fake-new"})
	if err != nil {
		t.Fatalf("Run() override error = %v", err)
	}
	if result.Answer != "13 * 7 = 91" {
		t.Fatalf("Run() Answer = %q, want calculator answer", result.Answer)
	}
	resolved, err := store.Resolve(ctx, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Config.ProviderName != "fake-new" {
		t.Fatalf("persisted ProviderName = %q, want fake-new", resolved.Config.ProviderName)
	}
}

func TestSessionServiceStreamAllowsExplicitProviderOverrideAndPersistsAfterSuccess(t *testing.T) {
	ctx := context.Background()
	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	configPath := writeProvidersConfigContent(t, `llms:
  default_provider: missing-default
  providers:
    fake-old:
      type: openai_compatible
      base_url: "https://example.invalid/v1"
      api_key_env: TEST_PROVIDER_KEY
      model: bad
    fake-new:
      type: fake
      model: fake-tool-model
`)
	svc, err := appservice.NewSessionService(appservice.SessionConfig{Store: store, ProviderConfigPath: configPath, ProviderName: "fake-old"})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	created, err := svc.Create(ctx, "stream switch provider")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	events, err := svc.Stream(ctx, created.ID, "use calculator to compute 13 * 7", appservice.RunOptions{ProviderName: "fake-new"})
	if err != nil {
		t.Fatalf("Stream() override error = %v", err)
	}
	got := collectStreamEvents(t, events)
	assertHasStreamEvent(t, got, "done", map[string]any{"answer": "13 * 7 = 91"})
	resolved, err := store.Resolve(ctx, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Config.ProviderName != "fake-new" {
		t.Fatalf("persisted ProviderName = %q, want fake-new", resolved.Config.ProviderName)
	}
}

func TestSessionServiceRunDoesNotPersistExplicitProviderOverrideAfterOverrideFailure(t *testing.T) {
	ctx := context.Background()
	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	configPath := writeProvidersConfigContent(t, `llms:
  default_provider: fake-old
  providers:
    fake-old:
      type: fake
      model: fake-tool-model
`)
	svc, err := appservice.NewSessionService(appservice.SessionConfig{Store: store, ProviderConfigPath: configPath, ProviderName: "fake-old"})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	created, err := svc.Create(ctx, "failed provider switch")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = svc.Run(ctx, created.ID, "use calculator to compute 13 * 7", appservice.RunOptions{ProviderName: "missing-provider"})
	if err == nil || !strings.Contains(err.Error(), `unknown provider "missing-provider"`) {
		t.Fatalf("Run() error = %v, want unknown provider error", err)
	}
	resolved, err := store.Resolve(ctx, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Config.ProviderName != "fake-old" {
		t.Fatalf("persisted ProviderName = %q, want fake-old after failed run", resolved.Config.ProviderName)
	}
}

func TestSessionServiceRunMissingStoredProviderRequiresExplicitOverride(t *testing.T) {
	ctx := context.Background()
	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	configPath := writeProvidersConfigContent(t, `llms:
  default_provider: fake-new
  providers:
    fake-new:
      type: fake
      model: fake-tool-model
`)
	creator, err := appservice.NewSessionService(appservice.SessionConfig{Store: store, ProviderConfigPath: configPath, ProviderName: "missing-provider"})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	created, err := creator.Create(ctx, "missing provider")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = creator.Run(ctx, created.ID, "use calculator to compute 13 * 7")
	if err == nil || !strings.Contains(err.Error(), `session "`+created.ID+`" provider "missing-provider" is not configured; specify provider_name to choose another provider`) {
		t.Fatalf("Run() error = %v, want actionable missing provider error", err)
	}
}

func TestSessionServiceRunResumesExistingTranscriptForNextPrompt(t *testing.T) {
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
	created, err := svc.Create(ctx, "resume calculator")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	first, err := svc.Run(ctx, created.ID, "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if first.Answer != "13 * 7 = 91" {
		t.Fatalf("first Run() Answer = %q, want 13 * 7 = 91", first.Answer)
	}
	second, err := svc.Run(ctx, created.ID, "what was the previous result?")
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if second.Answer != "previous result was 91" {
		t.Fatalf("second Run() Answer = %q, want previous result was 91", second.Answer)
	}
	detail, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertServiceResumedTranscript(t, detail.Messages)
}

func TestSessionServiceConcurrentRunOnSameSessionKeepsBothTurns(t *testing.T) {
	ctx := context.Background()
	server, arrivals, releaseRuns := newBlockingOpenAICompatibleServer(t)
	defer server.Close()

	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: writeOpenAICompatibleProvidersConfig(t, server.URL+"/v1"),
		ProviderName:       "test-openai",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	created, err := svc.Create(ctx, "concurrent run")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	results := make(chan runOutcome, 2)
	runPrompt := func(prompt string) {
		result, err := svc.Run(ctx, created.ID, prompt)
		results <- runOutcome{prompt: prompt, answer: result.Answer, err: err}
	}

	go runPrompt("first concurrent prompt")
	if got := receivePromptArrival(t, arrivals); got != "first concurrent prompt" {
		t.Fatalf("first provider prompt = %q, want first concurrent prompt", got)
	}

	go runPrompt("second concurrent prompt")
	secondArrivedBeforeRelease := false
	select {
	case got := <-arrivals:
		secondArrivedBeforeRelease = true
		if got != "second concurrent prompt" {
			t.Fatalf("second provider prompt = %q, want second concurrent prompt", got)
		}
	case <-time.After(250 * time.Millisecond):
	}
	releaseRuns()
	if !secondArrivedBeforeRelease {
		if got := receivePromptArrival(t, arrivals); got != "second concurrent prompt" {
			t.Fatalf("second provider prompt = %q, want second concurrent prompt", got)
		}
	}

	wantAnswers := map[string]string{
		"first concurrent prompt":  "first concurrent answer",
		"second concurrent prompt": "second concurrent answer",
	}
	for range wantAnswers {
		select {
		case outcome := <-results:
			if outcome.err != nil {
				t.Fatalf("Run(%q) error = %v", outcome.prompt, outcome.err)
			}
			if outcome.answer != wantAnswers[outcome.prompt] {
				t.Fatalf("Run(%q) Answer = %q, want %q", outcome.prompt, outcome.answer, wantAnswers[outcome.prompt])
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent Run results")
		}
	}

	detail, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(detail.Messages) != 4 {
		t.Fatalf("Get() Messages len = %d, want both user/assistant turns: %#v", len(detail.Messages), detail.Messages)
	}
	gotTurns := map[string]string{}
	for i := 0; i < len(detail.Messages); i += 2 {
		user := detail.Messages[i]
		assistant := detail.Messages[i+1]
		if user.Role != "user" || assistant.Role != "assistant" {
			t.Fatalf("messages[%d:%d] = %#v, %#v; want user then assistant", i, i+2, user, assistant)
		}
		gotTurns[user.Content] = assistant.Content
	}
	for prompt, answer := range wantAnswers {
		if gotTurns[prompt] != answer {
			t.Fatalf("persisted turn for %q = %q, want %q in %#v", prompt, gotTurns[prompt], answer, detail.Messages)
		}
	}
}

func TestSessionServiceConcurrentRunOnDifferentSessionsDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	server, arrivals, releaseRuns := newBlockingOpenAICompatibleServer(t)
	defer server.Close()

	store := appdata.NewManagerSessionStore(filepath.Join(t.TempDir(), "sessions"))
	svc, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              store,
		ProviderConfigPath: writeOpenAICompatibleProvidersConfig(t, server.URL+"/v1"),
		ProviderName:       "test-openai",
	})
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}
	firstSession, err := svc.Create(ctx, "first concurrent run")
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	secondSession, err := svc.Create(ctx, "second concurrent run")
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}

	results := make(chan runOutcome, 2)
	runPrompt := func(sessionID string, prompt string) {
		result, err := svc.Run(ctx, sessionID, prompt)
		results <- runOutcome{prompt: prompt, answer: result.Answer, err: err}
	}

	go runPrompt(firstSession.ID, "first concurrent prompt")
	if got := receivePromptArrival(t, arrivals); got != "first concurrent prompt" {
		t.Fatalf("first provider prompt = %q, want first concurrent prompt", got)
	}

	go runPrompt(secondSession.ID, "second concurrent prompt")
	select {
	case got := <-arrivals:
		if got != "second concurrent prompt" {
			t.Fatalf("second provider prompt = %q, want second concurrent prompt", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("different session run blocked behind active session")
	}
	releaseRuns()

	wantAnswers := map[string]string{
		"first concurrent prompt":  "first concurrent answer",
		"second concurrent prompt": "second concurrent answer",
	}
	for range wantAnswers {
		select {
		case outcome := <-results:
			if outcome.err != nil {
				t.Fatalf("Run(%q) error = %v", outcome.prompt, outcome.err)
			}
			if outcome.answer != wantAnswers[outcome.prompt] {
				t.Fatalf("Run(%q) Answer = %q, want %q", outcome.prompt, outcome.answer, wantAnswers[outcome.prompt])
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent Run results")
		}
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
	created, err := store.Create(ctx, "malformed tool args", session.SessionConfig{})
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

type capturedOpenAIChatRequest struct {
	Messages []capturedOpenAIChatMessage `json:"messages"`
}

type capturedOpenAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

type runOutcome struct {
	prompt string
	answer string
	err    error
}

func newBlockingOpenAICompatibleServer(t *testing.T) (*httptest.Server, <-chan string, func()) {
	t.Helper()
	arrivals := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRuns := func() {
		releaseOnce.Do(func() { close(release) })
	}
	t.Cleanup(releaseRuns)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		var captured capturedOpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode OpenAI-compatible request: %v", err)
		}
		prompt := lastUserPrompt(captured.Messages)
		arrivals <- prompt
		<-release
		answer := strings.Replace(prompt, " prompt", " answer", 1)
		writeServiceOpenAISSE(t, w,
			`{"choices":[{"delta":{"role":"assistant"}}]}`,
			fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, answer),
			`{"choices":[{"finish_reason":"stop"}]}`,
		)
	}))
	return server, arrivals, releaseRuns
}

func lastUserPrompt(messages []capturedOpenAIChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

func receivePromptArrival(t *testing.T, arrivals <-chan string) string {
	t.Helper()
	select {
	case prompt := <-arrivals:
		return prompt
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for provider request")
		return ""
	}
}
func newCapturingOpenAICompatibleServer(t *testing.T) (*httptest.Server, <-chan capturedOpenAIChatRequest) {
	t.Helper()
	requests := make(chan capturedOpenAIChatRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		var captured capturedOpenAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode OpenAI-compatible request: %v", err)
		}
		requests <- captured
		writeServiceOpenAISSE(t, w,
			`{"choices":[{"delta":{"role":"assistant"}}]}`,
			`{"choices":[{"delta":{"content":"managed answer"}}]}`,
			`{"choices":[{"finish_reason":"stop"}]}`,
		)
	}))
	return server, requests
}

func writeOpenAICompatibleProvidersConfig(t *testing.T, baseURL string) string {
	t.Helper()
	return writeProvidersConfigContent(t, fmt.Sprintf(`llms:
  default_provider: test-openai
  providers:
    test-openai:
      type: openai_compatible
      base_url: %q
      api_key_env: TEST_PROVIDER_KEY
      model: test-model
      timeout_seconds: 1
`, baseURL))
}

func writeServiceOpenAISSE(t *testing.T, w http.ResponseWriter, payloads ...string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("response writer does not support flushing")
	}
	for _, payload := range payloads {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			t.Fatalf("write SSE payload: %v", err)
		}
		flusher.Flush()
	}
}

func receiveOpenAIRequest(t *testing.T, requests <-chan capturedOpenAIChatRequest) capturedOpenAIChatRequest {
	t.Helper()
	select {
	case captured := <-requests:
		return captured
	default:
		t.Fatal("OpenAI-compatible server did not receive a chat completions request")
		return capturedOpenAIChatRequest{}
	}
}

func sessionPromptProviderContent(prompt string) string {
	return "Session prompt:\n" + prompt
}

func assertProviderReceivedSystemMessageBeforeUser(t *testing.T, messages []capturedOpenAIChatMessage, providerPrompt string, userPrompt string) {
	t.Helper()
	if len(messages) < 2 {
		t.Fatalf("provider messages = %#v, want system message followed by user prompt", messages)
	}
	if messages[0].Role != "system" || messages[0].Content != providerPrompt {
		t.Fatalf("first provider message = %#v, want provider prompt %q", messages[0], providerPrompt)
	}
	if messages[1].Role != "user" || messages[1].Content != userPrompt {
		t.Fatalf("second provider message = %#v, want user prompt %q", messages[1], userPrompt)
	}
}

func assertTranscriptDoesNotIncludeProviderPrompt(t *testing.T, messages []appservice.SessionMessage, providerPrompt string, userPrompt string) {
	t.Helper()
	if len(messages) != 2 {
		t.Fatalf("detail Messages len = %d, want user and assistant only: %#v", len(messages), messages)
	}
	if messages[0].Role != "user" || messages[0].Content != userPrompt {
		t.Fatalf("first transcript message = %#v, want user prompt %q", messages[0], userPrompt)
	}
	if messages[1].Role != "assistant" || messages[1].Content != "managed answer" {
		t.Fatalf("second transcript message = %#v, want assistant managed answer", messages[1])
	}
	for _, message := range messages {
		if message.Role == "system" || strings.Contains(message.Content, providerPrompt) {
			t.Fatalf("transcript leaks provider prompt %q in %#v", providerPrompt, messages)
		}
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

func TestSessionServiceCurrentProviderDiagnosticsReturnsConfigErrorForMissingBaseURL(t *testing.T) {
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

	_, err = svc.CurrentProviderDiagnostics(ctx)
	if err == nil || !strings.Contains(err.Error(), `provider "test-openai" openai_compatible base_url is required`) {
		t.Fatalf("CurrentProviderDiagnostics() error = %v, want missing base_url config error", err)
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

func assertServiceResumedTranscript(t *testing.T, messages []appservice.SessionMessage) {
	t.Helper()
	if len(messages) != 6 {
		t.Fatalf("detail Messages len = %d, want 6: %#v", len(messages), messages)
	}
	user := messages[0]
	if user.Role != "user" || user.Content != "use calculator to compute 13 * 7" {
		t.Fatalf("first user message = %#v, want calculator prompt", user)
	}
	assistantToolCall := messages[1]
	if assistantToolCall.Role != "assistant" || len(assistantToolCall.ToolCalls) != 1 {
		t.Fatalf("first assistant tool call message = %#v, want one calculator tool call", assistantToolCall)
	}
	call := assistantToolCall.ToolCalls[0]
	if call.ID != "call_fake_calculator" || call.Tool != "calculator" {
		t.Fatalf("first tool call = %#v, want calculator call", call)
	}
	assertJSONEqual(t, call.Arguments, `{"a":13,"b":7,"op":"mul"}`)
	toolResult := messages[2]
	if toolResult.Role != "tool" || toolResult.ToolCallID != call.ID || toolResult.Tool != "calculator" || toolResult.Content != "91" {
		t.Fatalf("first tool result message = %#v, want calculator result 91 for call %q", toolResult, call.ID)
	}
	firstAssistant := messages[3]
	if firstAssistant.Role != "assistant" || firstAssistant.Content != "13 * 7 = 91" || len(firstAssistant.ToolCalls) != 0 {
		t.Fatalf("first final assistant message = %#v, want final calculator answer", firstAssistant)
	}
	secondUser := messages[4]
	if secondUser.Role != "user" || secondUser.Content != "what was the previous result?" {
		t.Fatalf("second user message = %#v, want previous-result prompt", secondUser)
	}
	secondAssistant := messages[5]
	if secondAssistant.Role != "assistant" || secondAssistant.Content != "previous result was 91" || len(secondAssistant.ToolCalls) != 0 {
		t.Fatalf("second assistant message = %#v, want previous-result answer", secondAssistant)
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
