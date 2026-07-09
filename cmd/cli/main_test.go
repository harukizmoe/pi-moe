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
	"time"

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

func TestParseCLIOptionsAcceptsSessionFlag(t *testing.T) {
	got, err := parseCLIOptions([]string{"--session", "state/session.jsonl", "use", "calculator"})
	if err != nil {
		t.Fatalf("parseCLIOptions() error = %v", err)
	}

	if got.sessionPath != "state/session.jsonl" {
		t.Fatalf("sessionPath = %q, want state/session.jsonl", got.sessionPath)
	}
	if strings.Join(got.promptArgs, " ") != "use calculator" {
		t.Fatalf("promptArgs = %#v, want use calculator", got.promptArgs)
	}
}

func TestParseCLIOptionsAcceptsSessionLifecycleFlags(t *testing.T) {
	got, err := parseCLIOptions([]string{"--new-session", "first", "prompt"})
	if err != nil {
		t.Fatalf("parseCLIOptions() --new-session error = %v", err)
	}
	if !got.newSession {
		t.Fatal("newSession = false, want true")
	}
	if strings.Join(got.promptArgs, " ") != "first prompt" {
		t.Fatalf("promptArgs = %#v, want first prompt", got.promptArgs)
	}

	got, err = parseCLIOptions([]string{"--resume", "20260708-abc123", "next", "prompt"})
	if err != nil {
		t.Fatalf("parseCLIOptions() --resume error = %v", err)
	}
	if got.resumeSessionID != "20260708-abc123" {
		t.Fatalf("resumeSessionID = %q, want 20260708-abc123", got.resumeSessionID)
	}
	if strings.Join(got.promptArgs, " ") != "next prompt" {
		t.Fatalf("promptArgs = %#v, want next prompt", got.promptArgs)
	}

	got, err = parseCLIOptions([]string{"--list-sessions"})
	if err != nil {
		t.Fatalf("parseCLIOptions() --list-sessions error = %v", err)
	}
	if !got.listSessions {
		t.Fatal("listSessions = false, want true")
	}
}

func TestParseCLIOptionsRejectsConflictingSessionLifecycleFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "session and new", args: []string{"--session", "manual.jsonl", "--new-session", "prompt"}},
		{name: "session and resume", args: []string{"--session", "manual.jsonl", "--resume", "abc", "prompt"}},
		{name: "new and resume", args: []string{"--new-session", "--resume", "abc", "prompt"}},
		{name: "list and prompt", args: []string{"--list-sessions", "prompt"}},
		{name: "list and interactive", args: []string{"--list-sessions", "--interactive"}},
		{name: "list and session", args: []string{"--list-sessions", "--session", "manual.jsonl"}},
		{name: "list and new", args: []string{"--list-sessions", "--new-session"}},
		{name: "list and resume", args: []string{"--list-sessions", "--resume", "abc"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseCLIOptions(tt.args); err == nil {
				t.Fatalf("parseCLIOptions(%#v) error = nil, want conflict error", tt.args)
			}
		})
	}
}

func TestFormatSessionListOutput(t *testing.T) {
	metas := []session.SessionMeta{
		{
			ID:        "20260708-150405-a1b2c3",
			Title:     "use calculator to compute 13 * 7",
			UpdatedAt: time.Date(2026, 7, 8, 15, 4, 5, 0, time.UTC),
		},
		{
			ID:        "20260708-151210-d4e5f6",
			Title:     "second prompt",
			UpdatedAt: time.Date(2026, 7, 8, 15, 12, 10, 0, time.UTC),
		},
	}

	got := formatSessionListOutput(metas)
	want := "20260708-150405-a1b2c3  2026-07-08T15:04:05Z  use calculator to compute 13 * 7\n" +
		"20260708-151210-d4e5f6  2026-07-08T15:12:10Z  second prompt\n"
	if got != want {
		t.Fatalf("formatSessionListOutput() = %q, want %q", got, want)
	}
}

func TestFormatSessionListOutputEmpty(t *testing.T) {
	if got := formatSessionListOutput(nil); got != "" {
		t.Fatalf("formatSessionListOutput(nil) = %q, want empty", got)
	}
}

func TestCLISeparateSessionInstancesReuseTranscriptFromSessionFile(t *testing.T) {
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")

	firstOptions, err := parseCLIOptions([]string{
		"--config", providerConfigPath,
		"--session", sessionPath,
		"first prompt",
	})
	if err != nil {
		t.Fatalf("parseCLIOptions() first error = %v", err)
	}
	firstInput, err := readInput(firstOptions.promptArgs, strings.NewReader("ignored stdin"))
	if err != nil {
		t.Fatalf("readInput() first error = %v", err)
	}
	firstRunner, err := session.Open(context.Background(), session.Config{
		ProviderConfigPath: firstOptions.configPath,
		ProviderName:       firstOptions.providerName,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	}, firstOptions.sessionPath)
	if err != nil {
		t.Fatalf("session.Open() first error = %v", err)
	}
	firstOutput, err := collectRunOutput(firstRunner.Prompt(context.Background(), firstInput))
	if err != nil {
		t.Fatalf("collectRunOutput() first error = %v", err)
	}
	if firstOutput.Answer != "13 * 7 = 91" {
		t.Fatalf("first answer = %q, want 13 * 7 = 91", firstOutput.Answer)
	}

	secondOptions, err := parseCLIOptions([]string{
		"--config", providerConfigPath,
		"--session", sessionPath,
		"second prompt",
	})
	if err != nil {
		t.Fatalf("parseCLIOptions() second error = %v", err)
	}
	secondRunner, err := session.Open(context.Background(), session.Config{
		ProviderConfigPath: secondOptions.configPath,
		ProviderName:       secondOptions.providerName,
		Logger:             logger.NewNoop(),
		MaxSteps:           1,
	}, secondOptions.sessionPath)
	if err != nil {
		t.Fatalf("session.Open() second error = %v", err)
	}
	if got := collectUserPrompts(secondRunner.Messages()); !reflect.DeepEqual(got, []string{"first prompt"}) {
		t.Fatalf("reopened user prompts before second run = %#v, want first prompt", got)
	}

	secondInput, err := readInput(secondOptions.promptArgs, strings.NewReader("ignored stdin"))
	if err != nil {
		t.Fatalf("readInput() second error = %v", err)
	}
	secondOutput, err := collectRunOutput(secondRunner.Prompt(context.Background(), secondInput))
	if err != nil {
		t.Fatalf("collectRunOutput() second error = %v", err)
	}
	if secondOutput.Answer != "13 * 7 = 91" {
		t.Fatalf("second answer = %q, want 13 * 7 = 91", secondOutput.Answer)
	}
	if got := collectUserPrompts(secondRunner.Messages()); !reflect.DeepEqual(got, []string{"first prompt", "second prompt"}) {
		t.Fatalf("reopened user prompts after second run = %#v, want first and second prompt", got)
	}
}

func TestCLIManagedSessionNewAndResumeReuseTranscript(t *testing.T) {
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	sessionRoot := filepath.Join(t.TempDir(), "sessions")

	firstOptions, err := parseCLIOptions([]string{
		"--config", providerConfigPath,
		"--new-session",
		"first prompt",
	})
	if err != nil {
		t.Fatalf("parseCLIOptions() first error = %v", err)
	}
	firstInput, err := readInput(firstOptions.promptArgs, strings.NewReader("ignored stdin"))
	if err != nil {
		t.Fatalf("readInput() first error = %v", err)
	}
	firstRunner, firstMeta, err := newCLISessionWithRoot(context.Background(), firstOptions, logger.NewNoop(), sessionRoot, firstInput)
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() first error = %v", err)
	}
	firstOutput, err := collectRunOutput(firstRunner.Prompt(context.Background(), firstInput))
	if err != nil {
		t.Fatalf("collectRunOutput() first error = %v", err)
	}
	if firstOutput.Answer != "13 * 7 = 91" {
		t.Fatalf("first answer = %q, want 13 * 7 = 91", firstOutput.Answer)
	}
	if err := touchManagedSession(context.Background(), firstMeta); err != nil {
		t.Fatalf("touchManagedSession() first error = %v", err)
	}

	secondOptions, err := parseCLIOptions([]string{
		"--config", providerConfigPath,
		"--resume", firstMeta.ID,
		"second prompt",
	})
	if err != nil {
		t.Fatalf("parseCLIOptions() second error = %v", err)
	}
	secondInput, err := readInput(secondOptions.promptArgs, strings.NewReader("ignored stdin"))
	if err != nil {
		t.Fatalf("readInput() second error = %v", err)
	}
	secondRunner, secondMeta, err := newCLISessionWithRoot(context.Background(), secondOptions, logger.NewNoop(), sessionRoot, secondInput)
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() second error = %v", err)
	}
	if secondMeta.ID != firstMeta.ID {
		t.Fatalf("resumed meta ID = %q, want %q", secondMeta.ID, firstMeta.ID)
	}
	if got := collectUserPrompts(secondRunner.Messages()); !reflect.DeepEqual(got, []string{"first prompt"}) {
		t.Fatalf("reopened user prompts before second run = %#v, want first prompt", got)
	}
	secondOutput, err := collectRunOutput(secondRunner.Prompt(context.Background(), secondInput))
	if err != nil {
		t.Fatalf("collectRunOutput() second error = %v", err)
	}
	if secondOutput.Answer != "13 * 7 = 91" {
		t.Fatalf("second answer = %q, want 13 * 7 = 91", secondOutput.Answer)
	}
}

func TestNewCLISessionStoresProviderPreference(t *testing.T) {
	ctx := context.Background()
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	root := filepath.Join(t.TempDir(), "sessions")
	opts := cliOptions{configPath: providerConfigPath, providerName: "fake-local", newSession: true, promptArgs: []string{"hello"}}
	runner, managed, err := newCLISessionWithRoot(ctx, opts, logger.NewNoop(), root, "hello")
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() error = %v", err)
	}
	if runner == nil || managed == nil {
		t.Fatalf("runner/managed = %#v/%#v, want managed session", runner, managed)
	}
	meta, err := session.NewManager(root).Resolve(ctx, managed.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if meta.Config.ProviderName != "fake-local" {
		t.Fatalf("ProviderName = %q, want fake-local", meta.Config.ProviderName)
	}
}

func TestNewCLISessionWithoutProviderPinsResolvedDefaultProvider(t *testing.T) {
	ctx := context.Background()
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
    fake-alt:
      type: openai_compatible
      model: broken-default-model
`)
	root := filepath.Join(t.TempDir(), "sessions")
	opts := cliOptions{configPath: providerConfigPath, newSession: true, promptArgs: []string{"hello"}}
	runner, managed, err := newCLISessionWithRoot(ctx, opts, logger.NewNoop(), root, "hello")
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() error = %v", err)
	}
	if runner == nil || managed == nil {
		t.Fatalf("runner/managed = %#v/%#v, want managed session", runner, managed)
	}
	meta, err := session.NewManager(root).Resolve(ctx, managed.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if meta.Config.ProviderName != "fake-local" {
		t.Fatalf("ProviderName = %q, want resolved default fake-local", meta.Config.ProviderName)
	}

	if err := os.WriteFile(providerConfigPath, []byte(`llms:
  default_provider: fake-alt
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
    fake-alt:
      type: openai_compatible
      model: broken-default-model
`), 0o600); err != nil {
		t.Fatalf("rewrite providers config: %v", err)
	}
	resumeOpts := cliOptions{configPath: providerConfigPath, resumeSessionID: managed.ID, promptArgs: []string{"use calculator to compute 13 * 7"}}
	resumedRunner, resumedManaged, err := newCLISessionWithRoot(ctx, resumeOpts, logger.NewNoop(), root, "")
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() resume error = %v", err)
	}
	if resumedRunner == nil || resumedManaged == nil {
		t.Fatalf("resumed runner/managed = %#v/%#v, want managed session", resumedRunner, resumedManaged)
	}
	output, err := collectRunOutput(resumedRunner.Prompt(ctx, "use calculator to compute 13 * 7"))
	if err != nil {
		t.Fatalf("Prompt() after default_provider change error = %v", err)
	}
	if output.Answer != "13 * 7 = 91" {
		t.Fatalf("answer = %q, want pinned fake provider answer", output.Answer)
	}
}

func TestResumeCLISessionUsesStoredProviderPreference(t *testing.T) {
	ctx := context.Background()
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: missing-default
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	root := filepath.Join(t.TempDir(), "sessions")
	manager := session.NewManager(root)
	created, err := manager.Create(ctx, "resume", session.SessionConfig{ProviderName: "fake-local"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	opts := cliOptions{configPath: providerConfigPath, resumeSessionID: created.ID, promptArgs: []string{"use calculator to compute 13 * 7"}}
	runner, managed, err := newCLISessionWithRoot(ctx, opts, logger.NewNoop(), root, "")
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() error = %v", err)
	}
	if runner == nil || managed == nil {
		t.Fatalf("runner/managed = %#v/%#v, want managed session", runner, managed)
	}
	output, err := collectRunOutput(runner.Prompt(ctx, "use calculator to compute 13 * 7"))
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if output.Answer != "13 * 7 = 91" {
		t.Fatalf("answer = %q, want 13 * 7 = 91", output.Answer)
	}
}

func TestResumeCLISessionUsesExplicitProviderOverrideAndPersistsAfterTouch(t *testing.T) {
	ctx := context.Background()
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: missing-default
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	root := filepath.Join(t.TempDir(), "sessions")
	manager := session.NewManager(root)
	created, err := manager.Create(ctx, "resume", session.SessionConfig{ProviderName: "missing"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	opts := cliOptions{configPath: providerConfigPath, providerName: "fake-local", resumeSessionID: created.ID, promptArgs: []string{"use calculator to compute 13 * 7"}}
	runner, managed, err := newCLISessionWithRoot(ctx, opts, logger.NewNoop(), root, "")
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() override error = %v", err)
	}
	if _, err := collectRunOutput(runner.Prompt(ctx, "use calculator to compute 13 * 7")); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if err := touchManagedSession(ctx, managed); err != nil {
		t.Fatalf("touchManagedSession() error = %v", err)
	}
	resolved, err := manager.Resolve(ctx, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Config.ProviderName != "fake-local" {
		t.Fatalf("persisted ProviderName = %q, want fake-local", resolved.Config.ProviderName)
	}
}

func TestResumeCLISessionReportsMissingStoredProviderWithOverrideGuidance(t *testing.T) {
	ctx := context.Background()
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	root := filepath.Join(t.TempDir(), "sessions")
	manager := session.NewManager(root)
	created, err := manager.Create(ctx, "resume", session.SessionConfig{ProviderName: "removed-provider"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	opts := cliOptions{configPath: providerConfigPath, resumeSessionID: created.ID, promptArgs: []string{"hello"}}
	_, _, err = newCLISessionWithRoot(ctx, opts, logger.NewNoop(), root, "")
	if err == nil {
		t.Fatal("newCLISessionWithRoot() error = nil, want missing stored provider error")
	}
	want := `session "` + created.ID + `" provider "removed-provider" is not configured; specify --provider to choose another provider`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestParseCLIOptionsAcceptsManagedPreferenceFlags(t *testing.T) {
	got, err := parseCLIOptions([]string{
		"--max-steps", "2",
		"--system-prompt", "answer like a careful calculator",
		"--new-session",
		"compute (2 + 3) * 4",
	})
	if err != nil {
		t.Fatalf("parseCLIOptions() error = %v", err)
	}

	if gotMaxSteps := cliOptionInt(t, got, "maxSteps"); gotMaxSteps != 2 {
		t.Fatalf("maxSteps = %d, want 2", gotMaxSteps)
	}
	if gotSystemPrompt := cliOptionString(t, got, "systemPrompt"); gotSystemPrompt != "answer like a careful calculator" {
		t.Fatalf("systemPrompt = %q, want stored flag value", gotSystemPrompt)
	}
}

func TestNewCLISessionPersistsManagedPreferenceFlags(t *testing.T) {
	ctx := context.Background()
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	root := filepath.Join(t.TempDir(), "sessions")
	opts, err := parseCLIOptions([]string{
		"--config", providerConfigPath,
		"--provider", "fake-local",
		"--max-steps", "2",
		"--system-prompt", "answer with only the final result",
		"--new-session",
		"use calculator to compute 13 * 7",
	})
	if err != nil {
		t.Fatalf("parseCLIOptions() error = %v", err)
	}
	input, err := readInput(opts.promptArgs, strings.NewReader("ignored stdin"))
	if err != nil {
		t.Fatalf("readInput() error = %v", err)
	}
	runner, managed, err := newCLISessionWithRoot(ctx, opts, logger.NewNoop(), root, input)
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() error = %v", err)
	}
	if runner == nil || managed == nil {
		t.Fatalf("runner/managed = %#v/%#v, want managed session", runner, managed)
	}
	output, err := collectRunOutput(runner.Prompt(ctx, input))
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if output.Answer != "13 * 7 = 91" {
		t.Fatalf("answer = %q, want 13 * 7 = 91", output.Answer)
	}
	if err := touchManagedSession(ctx, managed); err != nil {
		t.Fatalf("touchManagedSession() error = %v", err)
	}

	meta, err := session.NewManager(root).Resolve(ctx, managed.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if meta.Config.MaxSteps != 2 {
		t.Fatalf("persisted MaxSteps = %d, want 2", meta.Config.MaxSteps)
	}
	if meta.Config.SystemPrompt != "answer with only the final result" {
		t.Fatalf("persisted SystemPrompt = %q, want flag value", meta.Config.SystemPrompt)
	}
	messages, err := session.LoadMessages(meta.Path)
	if err != nil {
		t.Fatalf("LoadMessages() error = %v", err)
	}
	if got := collectUserPrompts(messages); !reflect.DeepEqual(got, []string{"use calculator to compute 13 * 7"}) {
		t.Fatalf("persisted user prompts = %#v, want only CLI input", got)
	}
}

func TestResumeCLISessionUsesStoredMaxStepsPreference(t *testing.T) {
	ctx := context.Background()
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-two-tool-model
`)
	root := filepath.Join(t.TempDir(), "sessions")
	newOpts, err := parseCLIOptions([]string{
		"--config", providerConfigPath,
		"--provider", "fake-local",
		"--max-steps", "1",
		"--new-session",
		"session title",
	})
	if err != nil {
		t.Fatalf("parse new options: %v", err)
	}
	_, managed, err := newCLISessionWithRoot(ctx, newOpts, logger.NewNoop(), root, strings.Join(newOpts.promptArgs, " "))
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() new error = %v", err)
	}
	if err := touchManagedSession(ctx, managed); err != nil {
		t.Fatalf("touchManagedSession() new error = %v", err)
	}

	resumeOpts, err := parseCLIOptions([]string{
		"--config", providerConfigPath,
		"--resume", managed.ID,
		"compute (2 + 3) * 4",
	})
	if err != nil {
		t.Fatalf("parse resume options: %v", err)
	}
	runner, resumed, err := newCLISessionWithRoot(ctx, resumeOpts, logger.NewNoop(), root, strings.Join(resumeOpts.promptArgs, " "))
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() resume error = %v", err)
	}
	if resumed == nil || resumed.ID != managed.ID {
		t.Fatalf("resumed managed session = %#v, want id %q", resumed, managed.ID)
	}
	output, err := collectRunOutput(runner.Prompt(ctx, "compute (2 + 3) * 4"))
	if err == nil {
		t.Fatalf("Prompt() error = nil with output %#v, want stored max-steps error", output)
	}
	if !strings.Contains(err.Error(), "max steps") {
		t.Fatalf("Prompt() error = %v, want max steps", err)
	}
}

func TestResumeCLISessionPersistsExplicitManagedPreferenceOverrides(t *testing.T) {
	ctx := context.Background()
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-two-tool-model
`)
	root := filepath.Join(t.TempDir(), "sessions")
	manager := session.NewManager(root)
	created, err := manager.Create(ctx, "resume", session.SessionConfig{ProviderName: "fake-local", MaxSteps: 1, SystemPrompt: "stored prompt"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	opts, err := parseCLIOptions([]string{
		"--config", providerConfigPath,
		"--max-steps", "2",
		"--system-prompt", "override prompt",
		"--resume", created.ID,
		"compute (2 + 3) * 4",
	})
	if err != nil {
		t.Fatalf("parseCLIOptions() error = %v", err)
	}
	runner, managed, err := newCLISessionWithRoot(ctx, opts, logger.NewNoop(), root, strings.Join(opts.promptArgs, " "))
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() error = %v", err)
	}
	output, err := collectRunOutput(runner.Prompt(ctx, "compute (2 + 3) * 4"))
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if output.Answer != "final answer: 20" {
		t.Fatalf("answer = %q, want final answer: 20", output.Answer)
	}
	if err := touchManagedSession(ctx, managed); err != nil {
		t.Fatalf("touchManagedSession() error = %v", err)
	}
	resolved, err := manager.Resolve(ctx, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Config.MaxSteps != 2 {
		t.Fatalf("persisted MaxSteps = %d, want override 2", resolved.Config.MaxSteps)
	}
	if resolved.Config.SystemPrompt != "override prompt" {
		t.Fatalf("persisted SystemPrompt = %q, want override prompt", resolved.Config.SystemPrompt)
	}
}

func TestCLIManagedResumeAppendsToIndexedSession(t *testing.T) {
	ctx := context.Background()
	providerConfigPath := writeCLIProvidersConfig(t, `llms:
  default_provider: fake-local
  providers:
    fake-local:
      type: fake
      model: fake-tool-model
`)
	root := filepath.Join(t.TempDir(), "sessions")
	clock := time.Date(2026, 7, 8, 13, 0, 0, 0, time.UTC)
	session.SetNowForTest(t, func() time.Time { return clock })
	newOpts, err := parseCLIOptions([]string{"--config", providerConfigPath, "--provider", "fake-local", "--new-session", "use calculator to compute 13 * 7"})
	if err != nil {
		t.Fatalf("parse new options: %v", err)
	}
	firstRunner, managed, err := newCLISessionWithRoot(ctx, newOpts, logger.NewNoop(), root, strings.Join(newOpts.promptArgs, " "))
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() first error = %v", err)
	}
	firstOutput, err := collectRunOutput(firstRunner.Prompt(ctx, "use calculator to compute 13 * 7"))
	if err != nil {
		t.Fatalf("first collectRunOutput() error = %v", err)
	}
	if firstOutput.Answer != "13 * 7 = 91" {
		t.Fatalf("first answer = %q, want 13 * 7 = 91", firstOutput.Answer)
	}
	if err := touchManagedSession(ctx, managed); err != nil {
		t.Fatalf("first touch error = %v", err)
	}
	manager := session.NewManager(root)
	before, err := manager.Resolve(ctx, managed.ID)
	if err != nil {
		t.Fatalf("Resolve() before error = %v", err)
	}
	clock = clock.Add(time.Second)

	resumeOpts, err := parseCLIOptions([]string{"--config", providerConfigPath, "--provider", "fake-local", "--resume", managed.ID, "what was the previous result?"})
	if err != nil {
		t.Fatalf("parse resume options: %v", err)
	}
	secondRunner, resumed, err := newCLISessionWithRoot(ctx, resumeOpts, logger.NewNoop(), root, strings.Join(resumeOpts.promptArgs, " "))
	if err != nil {
		t.Fatalf("newCLISessionWithRoot() resume error = %v", err)
	}
	if resumed == nil || resumed.ID != managed.ID {
		t.Fatalf("resumed managed session = %#v, want id %q", resumed, managed.ID)
	}
	secondOutput, err := collectRunOutput(secondRunner.Prompt(ctx, "what was the previous result?"))
	if err != nil {
		t.Fatalf("second collectRunOutput() error = %v", err)
	}
	if secondOutput.Answer != "previous result was 91" {
		t.Fatalf("second answer = %q, want previous result was 91", secondOutput.Answer)
	}
	if err := touchManagedSession(ctx, resumed); err != nil {
		t.Fatalf("second touch error = %v", err)
	}
	after, err := manager.Resolve(ctx, managed.ID)
	if err != nil {
		t.Fatalf("Resolve() after error = %v", err)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated_at = %s, want after %s", after.UpdatedAt, before.UpdatedAt)
	}
	messages, err := session.LoadMessages(after.Path)
	if err != nil {
		t.Fatalf("LoadMessages() error = %v", err)
	}
	if len(messages) != 6 {
		t.Fatalf("messages len = %d, want 6: %#v", len(messages), messages)
	}
	firstUser, ok := messages[0].(agent.UserMessage)
	if !ok || firstUser.Content != "use calculator to compute 13 * 7" {
		t.Fatalf("first persisted message = %#v, want calculator user prompt", messages[0])
	}
	firstAssistantToolCall, ok := messages[1].(agent.AssistantMessage)
	if !ok || len(firstAssistantToolCall.ToolCalls) != 1 {
		t.Fatalf("first assistant persisted message = %#v, want one calculator tool call", messages[1])
	}
	toolResult, ok := messages[2].(agent.ToolResultMessage)
	if !ok || toolResult.ToolCallID != firstAssistantToolCall.ToolCalls[0].ID || toolResult.Content != "91" {
		t.Fatalf("tool result persisted message = %#v, want calculator result 91", messages[2])
	}
	firstAssistantAnswer, ok := messages[3].(agent.AssistantMessage)
	if !ok || firstAssistantAnswer.Content != "13 * 7 = 91" || len(firstAssistantAnswer.ToolCalls) != 0 {
		t.Fatalf("first assistant answer persisted message = %#v, want final calculator answer", messages[3])
	}
	secondUser, ok := messages[4].(agent.UserMessage)
	if !ok || secondUser.Content != "what was the previous result?" {
		t.Fatalf("second persisted user message = %#v, want previous-result prompt", messages[4])
	}
	secondAssistant, ok := messages[5].(agent.AssistantMessage)
	if !ok || secondAssistant.Content != "previous result was 91" || len(secondAssistant.ToolCalls) != 0 {
		t.Fatalf("second persisted assistant message = %#v, want previous-result answer", messages[5])
	}
}

func TestRunListSessionsUsesManagerIndex(t *testing.T) {
	sessionRoot := filepath.Join(t.TempDir(), "sessions")
	manager := session.NewManager(sessionRoot)
	first, err := manager.Create(context.Background(), "first prompt", session.SessionConfig{})
	if err != nil {
		t.Fatalf("Create() first error = %v", err)
	}
	second, err := manager.Create(context.Background(), "second prompt", session.SessionConfig{})
	if err != nil {
		t.Fatalf("Create() second error = %v", err)
	}
	time.Sleep(time.Millisecond)
	if err := manager.Touch(context.Background(), first.ID); err != nil {
		t.Fatalf("Touch() first error = %v", err)
	}

	var output bytes.Buffer
	if err := runListSessions(context.Background(), &output, sessionRoot); err != nil {
		t.Fatalf("runListSessions() error = %v", err)
	}

	got := output.String()
	if !strings.Contains(got, first.ID+"  ") || !strings.Contains(got, second.ID+"  ") {
		t.Fatalf("runListSessions() output = %q, want both ids %q and %q", got, first.ID, second.ID)
	}
	if strings.Index(got, first.ID) > strings.Index(got, second.ID) {
		t.Fatalf("runListSessions() output = %q, want touched first before second", got)
	}
}

func TestRunListSessionsWithMissingIndexPrintsNothing(t *testing.T) {
	var output bytes.Buffer
	if err := runListSessions(context.Background(), &output, filepath.Join(t.TempDir(), "sessions")); err != nil {
		t.Fatalf("runListSessions() error = %v", err)
	}
	if output.String() != "" {
		t.Fatalf("runListSessions() output = %q, want empty", output.String())
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

func cliOptionInt(t *testing.T, opts cliOptions, name string) int {
	t.Helper()
	field := reflect.ValueOf(opts).FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("cliOptions.%s is missing", name)
	}
	if field.Kind() != reflect.Int {
		t.Fatalf("cliOptions.%s kind = %s, want int", name, field.Kind())
	}
	return int(field.Int())
}

func cliOptionString(t *testing.T, opts cliOptions, name string) string {
	t.Helper()
	field := reflect.ValueOf(opts).FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("cliOptions.%s is missing", name)
	}
	if field.Kind() != reflect.String {
		t.Fatalf("cliOptions.%s kind = %s, want string", name, field.Kind())
	}
	return field.String()
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
