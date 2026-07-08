package service_test

import (
	"context"
	"os"
	"path/filepath"
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
