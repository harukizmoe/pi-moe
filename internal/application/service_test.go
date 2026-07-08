package application_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"harukizmoe/pimoe/internal/application"
)

func TestServiceCreateSessionUsesManagedSessionRootAndConfiguredProvider(t *testing.T) {
	ctx := context.Background()
	sessionRoot := filepath.Join(t.TempDir(), "sessions")
	service := newFakeService(t, sessionRoot)

	created, err := service.CreateSession(ctx, "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if created.ID == "" {
		t.Fatal("CreateSession() ID is empty")
	}
	if created.Title != "use calculator to compute 13 * 7" {
		t.Fatalf("CreateSession() Title = %q, want prompt-derived title", created.Title)
	}
	if filepath.Dir(created.Path) != sessionRoot {
		t.Fatalf("CreateSession() Path dir = %q, want %q", filepath.Dir(created.Path), sessionRoot)
	}
	if filepath.Ext(created.Path) != ".jsonl" {
		t.Fatalf("CreateSession() Path = %q, want .jsonl extension", created.Path)
	}

	listed, err := service.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListSessions() len = %d, want 1", len(listed))
	}
	if listed[0].ID != created.ID || listed[0].Path != created.Path || listed[0].Title != created.Title {
		t.Fatalf("ListSessions()[0] = %#v, want created session %#v", listed[0], created)
	}
}

func TestServiceListSessionsReturnsNewestCreatedSessionFirst(t *testing.T) {
	ctx := context.Background()
	service := newFakeService(t, filepath.Join(t.TempDir(), "sessions"))

	first, err := service.CreateSession(ctx, "first prompt")
	if err != nil {
		t.Fatalf("CreateSession() first error = %v", err)
	}
	second, err := service.CreateSession(ctx, "second prompt")
	if err != nil {
		t.Fatalf("CreateSession() second error = %v", err)
	}

	listed, err := service.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("ListSessions() len = %d, want 2", len(listed))
	}
	gotIDs := []string{listed[0].ID, listed[1].ID}
	wantIDs := []string{second.ID, first.ID}
	if gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] {
		t.Fatalf("ListSessions() ids = %#v, want newest created session first %#v", gotIDs, wantIDs)
	}
}

func TestServiceRunUsesExistingSessionAndReturnsAnswerWithToolStep(t *testing.T) {
	ctx := context.Background()
	service := newFakeService(t, filepath.Join(t.TempDir(), "sessions"))

	created, err := service.CreateSession(ctx, "calculator session")
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	result, err := service.Run(ctx, created.ID, "use calculator to compute 13 * 7")
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
	if step.ToolName != "calculator" {
		t.Fatalf("Run() ToolSteps[0].ToolName = %q, want calculator", step.ToolName)
	}
	if step.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("Run() ToolSteps[0].Arguments = %q, want fake calculator arguments", step.Arguments)
	}
	if step.Result != "91" {
		t.Fatalf("Run() ToolSteps[0].Result = %q, want 91", step.Result)
	}
}

func newFakeService(t *testing.T, sessionRoot string) *application.Service {
	t.Helper()

	service, err := application.NewService(application.Config{
		SessionRoot:        sessionRoot,
		ProviderConfigPath: writeProvidersConfig(t),
		ProviderName:       "fake-local",
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func writeProvidersConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "providers.yaml")
	content := `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write providers config: %v", err)
	}
	return path
}
