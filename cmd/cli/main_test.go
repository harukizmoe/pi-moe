package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/session"
)

func TestReadInputJoinsArgsIntoPrompt(t *testing.T) {
	got, err := readInput([]string{"use", "calculator", "to", "compute", "13", "*", "7"}, strings.NewReader("ignored stdin"))
	if err != nil {
		t.Fatalf("readInput() error = %v", err)
	}

	const want = "use calculator to compute 13 * 7"
	if got != want {
		t.Fatalf("readInput() = %q, want %q", got, want)
	}
}

func TestReadInputReadsPromptFromStdinWhenArgsEmpty(t *testing.T) {
	got, err := readInput(nil, strings.NewReader("prompt from stdin\n"))
	if err != nil {
		t.Fatalf("readInput() error = %v", err)
	}

	const want = "prompt from stdin"
	if got != want {
		t.Fatalf("readInput() = %q, want %q", got, want)
	}
}

func TestReadInputRejectsEmptyOrWhitespaceInput(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		stdin string
	}{
		{name: "no args and empty stdin", stdin: ""},
		{name: "no args and whitespace stdin", stdin: "  \n\t  "},
		{name: "whitespace args", args: []string{" ", "\t"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readInput(tt.args, strings.NewReader(tt.stdin))
			if err == nil {
				t.Fatalf("readInput() error = nil, got %q", got)
			}
		})
	}
}

func TestParseCLIOptionsDefaults(t *testing.T) {
	got, err := parseCLIOptions([]string{"use", "calculator"})
	if err != nil {
		t.Fatalf("parseCLIOptions() error = %v", err)
	}

	if got.configPath != defaultCLIProviderConfigPath {
		t.Fatalf("configPath = %q, want %q", got.configPath, defaultCLIProviderConfigPath)
	}
	if got.providerName != "" {
		t.Fatalf("providerName = %q, want empty", got.providerName)
	}
	if got.includeTrace {
		t.Fatal("includeTrace = true, want false")
	}
	if strings.Join(got.promptArgs, " ") != "use calculator" {
		t.Fatalf("promptArgs = %#v, want use calculator", got.promptArgs)
	}
}

func TestParseCLIOptionsAcceptsSmokeFlags(t *testing.T) {
	got, err := parseCLIOptions([]string{
		"--config", "testdata/providers.yaml",
		"--provider", "moeco",
		"--trace",
		"use", "calculator",
	})
	if err != nil {
		t.Fatalf("parseCLIOptions() error = %v", err)
	}

	if got.configPath != "testdata/providers.yaml" {
		t.Fatalf("configPath = %q", got.configPath)
	}
	if got.providerName != "moeco" {
		t.Fatalf("providerName = %q, want moeco", got.providerName)
	}
	if !got.includeTrace {
		t.Fatal("includeTrace = false, want true")
	}
	if strings.Join(got.promptArgs, " ") != "use calculator" {
		t.Fatalf("promptArgs = %#v, want use calculator", got.promptArgs)
	}
}

func TestParseCLIOptionsAcceptsInteractiveFlagWithoutSwallowingPromptArgs(t *testing.T) {
	got, err := parseCLIOptions([]string{"--interactive", "use", "calculator"})
	if err != nil {
		t.Fatalf("parseCLIOptions() error = %v", err)
	}

	if !got.interactive {
		t.Fatal("interactive = false, want true")
	}
	if strings.Join(got.promptArgs, " ") != "use calculator" {
		t.Fatalf("promptArgs = %#v, want use calculator", got.promptArgs)
	}
	if got.includeTrace {
		t.Fatal("includeTrace = true, want false")
	}
}

func TestRunInteractiveReusesSessionAcrossTurnsUntilQuit(t *testing.T) {
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)

	runner, err := session.New(context.Background(), session.Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	})
	if err != nil {
		t.Fatalf("session.New() error = %v", err)
	}

	input := strings.NewReader("first prompt\nsecond prompt\nquit\n")
	var output bytes.Buffer

	if err := runInteractive(context.Background(), runner, input, &output, false); err != nil {
		t.Fatalf("runInteractive() error = %v", err)
	}

	if got := strings.Count(output.String(), "13 * 7 = 91\n"); got != 2 {
		t.Fatalf("final answer occurrences = %d, want 2 in %q", got, output.String())
	}

	if got := collectUserPrompts(runner.Messages()); !reflect.DeepEqual(got, []string{"first prompt", "second prompt"}) {
		t.Fatalf("session user prompts = %#v, want first and second prompt", got)
	}
}

func TestRunInteractiveAcceptsPromptLongerThanScannerTokenLimit(t *testing.T) {
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)

	runner, err := session.New(context.Background(), session.Config{
		ProviderConfigPath: providerConfigPath,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	})
	if err != nil {
		t.Fatalf("session.New() error = %v", err)
	}

	longPrompt := strings.Repeat("x", 70*1024)
	input := strings.NewReader(longPrompt + "\nquit\n")
	var output bytes.Buffer

	if err := runInteractive(context.Background(), runner, input, &output, false); err != nil {
		t.Fatalf("runInteractive() error for %d-byte line = %v", len(longPrompt), err)
	}

	if got := strings.Count(output.String(), "13 * 7 = 91\n"); got != 1 {
		t.Fatalf("final answer occurrences = %d, want 1 in %q", got, output.String())
	}

	prompts := collectUserPrompts(runner.Messages())
	if len(prompts) != 1 {
		t.Fatalf("session user prompts len = %d, want 1", len(prompts))
	}
	if prompts[0] != longPrompt {
		t.Fatalf("stored prompt len = %d, want %d", len(prompts[0]), len(longPrompt))
	}
}

func TestCollectRunOutputAnswerOnlyReturnsAnswerWithTrailingNewline(t *testing.T) {
	output, err := collectRunOutput(eventStream(
		session.MessageEndEvent{Message: agent.AssistantMessage{Content: "done without tools"}},
		session.RunEndEvent{RunID: "run-1"},
	))
	if err != nil {
		t.Fatalf("collectRunOutput() error = %v", err)
	}

	got := formatRunOutput(output, false)

	const want = "done without tools\n"
	if got != want {
		t.Fatalf("formatRunOutput(answer only) = %q, want %q", got, want)
	}
	if strings.Contains(got, "tool=") || strings.Contains(got, "arguments=") || strings.Contains(got, "result=") || strings.Contains(got, "error=") {
		t.Fatalf("formatRunOutput(answer only) leaked trace fields: %q", got)
	}
}

func TestCollectRunOutputTraceIncludesSuccessfulToolSteps(t *testing.T) {
	output, err := collectRunOutput(eventStream(
		session.ToolExecutionStartEvent{ToolCallID: "call-1", ToolName: "calculator", Arguments: `{"a":2,"b":3,"op":"add"}`},
		session.ToolExecutionEndEvent{ToolCallID: "call-1", Result: agent.ToolResultMessage{ToolCallID: "call-1", ToolName: "calculator", Content: "5"}},
		session.ToolExecutionStartEvent{ToolCallID: "call-2", ToolName: "calculator", Arguments: `{"a":5,"b":4,"op":"multiply"}`},
		session.ToolExecutionEndEvent{ToolCallID: "call-2", Result: agent.ToolResultMessage{ToolCallID: "call-2", ToolName: "calculator", Content: "20"}},
		session.MessageEndEvent{Message: agent.AssistantMessage{Content: "final answer: 20"}},
		session.RunEndEvent{RunID: "run-1"},
	))
	if err != nil {
		t.Fatalf("collectRunOutput() error = %v", err)
	}

	got := formatRunOutput(output, true)

	if !strings.HasPrefix(got, "final answer: 20\n") {
		t.Fatalf("formatRunOutput(trace) prefix = %q, want answer followed by newline", got)
	}
	for _, want := range []string{
		"tool=calculator",
		`arguments={"a":2,"b":3,"op":"add"}`,
		"result=5",
		`arguments={"a":5,"b":4,"op":"multiply"}`,
		"result=20",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatRunOutput(trace) missing %q in %q", want, got)
		}
	}
}

func TestCollectRunOutputTraceIncludesToolErrors(t *testing.T) {
	toolErr := errors.New("upstream unavailable")
	output, err := collectRunOutput(eventStream(
		session.ToolExecutionStartEvent{ToolCallID: "call-weather", ToolName: "weather", Arguments: `{"city":"Tokyo"}`},
		session.ToolExecutionEndEvent{ToolCallID: "call-weather", Result: agent.ToolResultMessage{ToolCallID: "call-weather", ToolName: "weather", Content: `tool "weather" failed: upstream unavailable`, IsError: true}, Error: toolErr},
		session.MessageEndEvent{Message: agent.AssistantMessage{Content: "could not complete weather lookup"}},
		session.RunEndEvent{RunID: "run-1"},
	))
	if err != nil {
		t.Fatalf("collectRunOutput() error = %v", err)
	}

	got := formatRunOutput(output, true)

	if !strings.HasPrefix(got, "could not complete weather lookup\n") {
		t.Fatalf("formatRunOutput(trace error) prefix = %q, want answer followed by newline", got)
	}
	for _, want := range []string{
		"tool=weather",
		`arguments={"city":"Tokyo"}`,
		"error=upstream unavailable",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatRunOutput(trace error) missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "result=<nil>") {
		t.Fatalf("formatRunOutput(trace error) exposed nil result placeholder: %q", got)
	}
}

func eventStream(events ...session.Event) <-chan session.Event {
	stream := make(chan session.Event, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return stream
}

func writeCLIProvidersConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "providers.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write providers config: %v", err)
	}
	return path
}

func collectUserPrompts(messages []agent.Message) []string {
	userPrompts := make([]string, 0, len(messages))
	for _, message := range messages {
		userMessage, ok := message.(agent.UserMessage)
		if ok {
			userPrompts = append(userPrompts, userMessage.Content)
		}
	}
	return userPrompts
}
