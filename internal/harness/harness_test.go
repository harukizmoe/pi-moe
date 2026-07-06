package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/logger"
)

func TestNewSessionUsesConfiguredFakeProviderAndCalculator(t *testing.T) {
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

	answer := collectHarnessAnswer(t, h.NewSession().Prompt(context.Background(), "use calculator to compute 13 * 7"))
	if answer != "13 * 7 = 91" {
		t.Fatalf("answer = %q, want %q", answer, "13 * 7 = 91")
	}
}

func TestNewUsesProviderNameOverride(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: bad-default
  providers:
    bad-default:
      type: does_not_exist
      model: broken-model
    fake-local:
      type: fake
      model: fake-tool-model
`)

	h, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		ProviderName:       "fake-local",
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	answer := collectHarnessAnswer(t, h.NewSession().Prompt(context.Background(), "use calculator to compute 13 * 7"))
	if answer != "13 * 7 = 91" {
		t.Fatalf("answer = %q, want %q", answer, "13 * 7 = 91")
	}
}

func TestNewReturnsErrorWhenProviderNameMissing(t *testing.T) {
	providerConfigPath := writeProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)

	_, err := New(context.Background(), Config{
		ProviderConfigPath: providerConfigPath,
		ProviderName:       "missing-provider",
		Logger:             logger.NewNoop(),
	})
	if err == nil {
		t.Fatal("New() error = nil, want unknown provider error")
	}
	if !strings.Contains(err.Error(), `unknown provider "missing-provider"`) {
		t.Fatalf("New() error = %v, want unknown provider message", err)
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
		t.Fatal("New() error = nil, want unknown provider error")
	}
	if !strings.Contains(err.Error(), `unknown provider "missing-provider"`) {
		t.Fatalf("New() error = %v, want unknown provider message", err)
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

func TestSessionPromptRejectsEmptyOrWhitespaceOnlyInput(t *testing.T) {
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
			session := h.NewSession()
			events := collectHarnessStreamEvents(t, session.Prompt(context.Background(), tt.input))
			if len(events) != 1 {
				t.Fatalf("Prompt(%q) events len = %d, want 1", tt.input, len(events))
			}
			errEvent, ok := events[0].(ErrorEvent)
			if !ok {
				t.Fatalf("Prompt(%q) event = %T, want ErrorEvent", tt.input, events[0])
			}
			if errEvent.Error == nil || !strings.Contains(errEvent.Error.Error(), "empty input") {
				t.Fatalf("Prompt(%q) error = %v, want empty input", tt.input, errEvent.Error)
			}
			if messages := session.Messages(); len(messages) != 0 {
				t.Fatalf("Messages() len = %d, want 0", len(messages))
			}
		})
	}
}

func TestSessionPromptTrimsSurroundingWhitespaceBeforePassingInputOnward(t *testing.T) {
	h := newFakeHarness(t)

	answer := collectHarnessAnswer(t, h.NewSession().Prompt(context.Background(), "  use calculator to compute 13 * 7  "))
	if answer != "13 * 7 = 91" {
		t.Fatalf("answer = %q, want %q", answer, "13 * 7 = 91")
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

func collectHarnessAnswer(t *testing.T, stream <-chan Event) string {
	t.Helper()
	var answer string
	for event := range stream {
		switch event := event.(type) {
		case MessageEndEvent:
			if len(event.Message.ToolCalls) == 0 {
				answer = event.Message.Content
			}
		case ErrorEvent:
			if event.Error != nil {
				t.Fatalf("stream error = %v", event.Error)
			}
		}
	}
	return answer
}

func writeProvidersConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "providers.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write providers config: %v", err)
	}
	return path
}
