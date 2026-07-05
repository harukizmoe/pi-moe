package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/logger"
)

func TestNewRunUsesConfiguredFakeProviderAndCalculator(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)

	var events []agent.Event
	h, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
		OnEvent: func(event agent.Event) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got, err := h.Run(context.Background(), "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got == nil {
		t.Fatal("Run() result = nil")
	}
	if got.Answer != "13 * 7 = 91" {
		t.Fatalf("Run().Answer = %q, want %q", got.Answer, "13 * 7 = 91")
	}
	if got.ToolRounds != 1 {
		t.Fatalf("Run().ToolRounds = %d, want 1", got.ToolRounds)
	}
	if len(got.Steps) != 1 {
		t.Fatalf("Run().Steps len = %d, want 1", len(got.Steps))
	}

	step := got.Steps[0]
	if step.ToolName != "calculator" {
		t.Fatalf("Run().Steps[0].ToolName = %q, want calculator", step.ToolName)
	}
	if step.Arguments != `{"a":13,"b":7,"op":"mul"}` {
		t.Fatalf("Run().Steps[0].Arguments = %q", step.Arguments)
	}
	if step.Result != "91" {
		t.Fatalf("Run().Steps[0].Result = %q, want 91", step.Result)
	}
	if step.Error != "" {
		t.Fatalf("Run().Steps[0].Error = %q, want empty", step.Error)
	}

	wantEventTypes := []agent.EventType{agent.EventToolCall, agent.EventToolResult, agent.EventFinal}
	if len(events) != len(wantEventTypes) {
		t.Fatalf("events len = %d, want %d", len(events), len(wantEventTypes))
	}
	for i, wantType := range wantEventTypes {
		if events[i].Type != wantType {
			t.Fatalf("events[%d].Type = %q, want %q", i, events[i].Type, wantType)
		}
	}
}

func TestNewReturnsErrorWhenDefaultProviderMissing(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: missing-provider
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)

	_, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
	})
	if err == nil {
		t.Fatal("New() error = nil, want unknown default provider error")
	}
	if !strings.Contains(err.Error(), "unknown default provider") {
		t.Fatalf("New() error = %v, want unknown default provider message", err)
	}
}

func TestNewReturnsErrorWhenProviderTypeUnknown(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: bad-provider
  providers:
    bad-provider:
      type: does_not_exist
      model: fake-tool-model
`)

	_, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
	})
	if err == nil {
		t.Fatal("New() error = nil, want unknown llm provider type error")
	}
	if !strings.Contains(err.Error(), "unknown llm provider type") {
		t.Fatalf("New() error = %v, want unknown llm provider type message", err)
	}
}

func TestRunRejectsEmptyOrWhitespaceOnlyInput(t *testing.T) {
	h := newFakeHarness(t)

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "spaces", input: "   "},
		{name: "mixed_whitespace", input: " \n\t "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.Run(context.Background(), tt.input)
			if err == nil {
				t.Fatalf("Run(%q) error = nil, want empty input error", tt.input)
			}
			if !strings.Contains(err.Error(), "empty input") {
				t.Fatalf("Run(%q) error = %v, want empty input message", tt.input, err)
			}
		})
	}
}

func TestRunTrimsSurroundingWhitespaceBeforePassingInputOnward(t *testing.T) {
	h := newFakeHarness(t)

	got, err := h.Run(context.Background(), "  use calculator to compute 13 * 7  ")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got == nil {
		t.Fatal("Run() result = nil")
	}
	if got.Answer != "13 * 7 = 91" {
		t.Fatalf("Run().Answer = %q, want %q", got.Answer, "13 * 7 = 91")
	}
}

func newFakeHarness(t *testing.T) *Harness {
	t.Helper()

	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)

	h, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return h
}
func writeProvidersConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "providers.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write providers config: %v", err)
	}
	return path
}
