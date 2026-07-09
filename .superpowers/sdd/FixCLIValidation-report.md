# FixCLIValidation Report

Status: implemented and verified.

Changes:
- Reject `--interactive` when prompt arguments are present, while preserving `--interactive` alone.
- Validate the resolved CLI session provider config before `manager.Create` for `--new-session`, preventing failed provider setup from writing a managed-session index entry.
- Updated the stale interactive CLI test and added a regression test for failed `--new-session` index cleanliness.
- Updated the `fake-alt` CLI fixture to satisfy strict `openai_compatible` config validation while preserving the stored-provider regression intent.

RED evidence:
- `go test ./cmd/cli -count=1` failed before implementation with `TestParseCLIOptionsValidatesInteractivePromptArgs/interactive_with_prompt_args`: `error = nil, want validation error`.
- `go test ./cmd/cli -count=1` failed before implementation with `TestNewManagedSessionWithInvalidProviderDoesNotDirtySessionIndex`: `runListSessions() output = "20260709-114945-5da500  2026-07-09T11:49:45Z  hello\n", want empty after invalid provider`.

GREEN evidence:
- `go test ./cmd/cli -count=1` -> `ok  	harukizmoe/pimoe/cmd/cli	0.021s`.

Files changed:
- `cmd/cli/main.go`
- `cmd/cli/main_test.go`
- `.superpowers/sdd/FixCLIValidation-report.md`

Risks: none known.
