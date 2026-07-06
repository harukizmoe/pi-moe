package main

import (
	"errors"
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/harness"
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

func TestCollectRunOutputAnswerOnlyReturnsAnswerWithTrailingNewline(t *testing.T) {
	output, err := collectRunOutput(eventStream(
		harness.MessageEndEvent{Message: agent.AssistantMessage{Content: "done without tools"}},
		harness.RunEndEvent{RunID: "run-1"},
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
		harness.ToolExecutionStartEvent{ToolCallID: "call-1", ToolName: "calculator", Arguments: `{"a":2,"b":3,"op":"add"}`},
		harness.ToolExecutionEndEvent{ToolCallID: "call-1", Result: agent.ToolResultMessage{ToolCallID: "call-1", ToolName: "calculator", Content: "5"}},
		harness.ToolExecutionStartEvent{ToolCallID: "call-2", ToolName: "calculator", Arguments: `{"a":5,"b":4,"op":"multiply"}`},
		harness.ToolExecutionEndEvent{ToolCallID: "call-2", Result: agent.ToolResultMessage{ToolCallID: "call-2", ToolName: "calculator", Content: "20"}},
		harness.MessageEndEvent{Message: agent.AssistantMessage{Content: "final answer: 20"}},
		harness.RunEndEvent{RunID: "run-1"},
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
		harness.ToolExecutionStartEvent{ToolCallID: "call-weather", ToolName: "weather", Arguments: `{"city":"Tokyo"}`},
		harness.ToolExecutionEndEvent{ToolCallID: "call-weather", Result: agent.ToolResultMessage{ToolCallID: "call-weather", ToolName: "weather", Content: `tool "weather" failed: upstream unavailable`, IsError: true}, Error: toolErr},
		harness.MessageEndEvent{Message: agent.AssistantMessage{Content: "could not complete weather lookup"}},
		harness.RunEndEvent{RunID: "run-1"},
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

func eventStream(events ...harness.Event) <-chan harness.Event {
	stream := make(chan harness.Event, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return stream
}
