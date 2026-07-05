package main

import (
	"strings"
	"testing"

	"harukizmoe/pimoe/internal/agent"
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

func TestFormatRunResultAnswerOnlyReturnsAnswerWithTrailingNewline(t *testing.T) {
	result := &agent.RunResult{
		Answer: "done without tools",
		Steps: []agent.Step{{
			ToolName:  "calculator",
			Arguments: `{"a":2,"b":3,"op":"add"}`,
			Result:    "5",
		}},
	}

	got := formatRunResult(result, false)

	const want = "done without tools\n"
	if got != want {
		t.Fatalf("formatRunResult(answer only) = %q, want %q", got, want)
	}
	if strings.Contains(got, "tool=") || strings.Contains(got, "arguments=") || strings.Contains(got, "result=") || strings.Contains(got, "error=") {
		t.Fatalf("formatRunResult(answer only) leaked trace fields: %q", got)
	}
}

func TestFormatRunResultTraceIncludesSuccessfulToolSteps(t *testing.T) {
	result := &agent.RunResult{
		Answer: "final answer: 20",
		Steps: []agent.Step{
			{
				ToolName:  "calculator",
				Arguments: `{"a":2,"b":3,"op":"add"}`,
				Result:    "5",
			},
			{
				ToolName:  "calculator",
				Arguments: `{"a":5,"b":4,"op":"multiply"}`,
				Result:    "20",
			},
		},
	}

	got := formatRunResult(result, true)

	if !strings.HasPrefix(got, "final answer: 20\n") {
		t.Fatalf("formatRunResult(trace) prefix = %q, want answer followed by newline", got)
	}
	for _, want := range []string{
		"tool=calculator",
		`arguments={"a":2,"b":3,"op":"add"}`,
		"result=5",
		`arguments={"a":5,"b":4,"op":"multiply"}`,
		"result=20",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatRunResult(trace) missing %q in %q", want, got)
		}
	}
}

func TestFormatRunResultTraceIncludesToolErrors(t *testing.T) {
	result := &agent.RunResult{
		Answer: "could not complete weather lookup",
		Steps: []agent.Step{{
			ToolName:  "weather",
			Arguments: `{"city":"Tokyo"}`,
			Error:     "upstream unavailable",
		}},
	}

	got := formatRunResult(result, true)

	if !strings.HasPrefix(got, "could not complete weather lookup\n") {
		t.Fatalf("formatRunResult(trace error) prefix = %q, want answer followed by newline", got)
	}
	for _, want := range []string{
		"tool=weather",
		`arguments={"city":"Tokyo"}`,
		"error=upstream unavailable",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatRunResult(trace error) missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "result=<nil>") {
		t.Fatalf("formatRunResult(trace error) exposed nil result placeholder: %q", got)
	}
}
