# Harness Provider Override and CLI Smoke Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `internal/harness` selectable by provider instance name and expose that capability through a thin CLI smoke driver with optional trace output.

**Architecture:** `internal/harness` remains the reusable Agent assembly boundary: it reads config, selects the provider instance, registers tools, and constructs the Agent. `cmd/cli` only parses smoke-driver flags, reads prompt input, calls Harness, and formats `RunResult`. Provider override is represented as `harness.Config.ProviderName`; CLI passes `--provider` through without touching provider registries.

**Tech Stack:** Go 1.26.4, standard library `flag`, existing `testing`, existing `internal/harness`, `internal/agent`, `internal/llms`, fake provider.

---

## File Structure

- Modify: `internal/harness/harness.go`
  - Add `Config.ProviderName`.
  - Select provider by `cfg.ProviderName` when non-empty; otherwise use YAML `llms.default_provider`.
  - Return one clear `unknown provider "<name>"` error for missing selected provider.
- Modify: `internal/harness/harness_test.go`
  - Add tests proving provider override happens inside Harness.
  - Update the missing-default-provider expectation to the unified `unknown provider` error.
- Modify: `cmd/cli/main.go`
  - Add `parseCLIOptions` using standard library `flag.FlagSet`.
  - Add `--config`, `--provider`, and `--trace` smoke flags.
  - Pass parsed config and provider values to `harness.Config`.
  - Pass parsed trace value to `formatRunResult`.
- Modify: `cmd/cli/main_test.go`
  - Add parser tests for defaults and explicit smoke flags.
  - Reuse existing `readInput` and `formatRunResult` tests.

---

### Task 1: Add Harness provider override

**Files:**
- Modify: `internal/harness/harness_test.go`
- Modify: `internal/harness/harness.go`

- [ ] **Step 1: Write failing Harness provider override tests**

Append these tests after `TestNewRunUsesConfiguredFakeProviderAndCalculator` in `internal/harness/harness_test.go`:

```go
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

	got, err := h.Run(context.Background(), "use calculator to compute 13 * 7")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got == nil || got.Answer != "13 * 7 = 91" {
		t.Fatalf("Run().Answer = %#v, want %q", got, "13 * 7 = 91")
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
```

Also update `TestNewReturnsErrorWhenDefaultProviderMissing` so the expectation matches the unified error:

```go
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
```

- [ ] **Step 2: Run Harness tests to verify failure**

Run:

```bash
go test ./internal/harness -run 'TestNewUsesProviderNameOverride|TestNewReturnsErrorWhenProviderNameMissing|TestNewReturnsErrorWhenDefaultProviderMissing' -v
```

Expected: FAIL because `Config` has no `ProviderName` field yet, or because the missing default provider error still says `unknown default provider`.

- [ ] **Step 3: Implement Harness provider selection**

In `internal/harness/harness.go`, update `Config` to include `ProviderName` after `ProviderConfigPath`:

```go
// Config 保存创建 Agent harness 所需的运行时依赖配置。
type Config struct {
	// ProviderConfigPath 是 providers YAML 配置路径；为空时使用默认本地配置。
	ProviderConfigPath string
	// ProviderName 覆盖配置中的默认 Provider 实例名；为空时使用 llms.default_provider。
	ProviderName string
	// Logger 接收 Agent 内部日志；为空时使用 no-op logger。
	Logger logger.Logger
	// MaxSteps 限制一次运行最多执行多少轮 tool calling；小于 1 时使用 Agent 默认值。
	MaxSteps int
	// OnEvent 接收 Agent 运行事件；为空时不发送事件。
	OnEvent agent.EventHandler
}
```

Replace the provider lookup block in `New`:

```go
	providerName := loaded.LLMs.DefaultProvider
	providerConfig, ok := loaded.LLMs.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown default provider %q", providerName)
	}
```

with:

```go
	providerName := cfg.ProviderName
	if providerName == "" {
		providerName = loaded.LLMs.DefaultProvider
	}
	providerConfig, ok := loaded.LLMs.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", providerName)
	}
```

- [ ] **Step 4: Run Harness tests to verify pass**

Run:

```bash
go test ./internal/harness -run 'TestNewUsesProviderNameOverride|TestNewReturnsErrorWhenProviderNameMissing|TestNewReturnsErrorWhenDefaultProviderMissing' -v
```

Expected: PASS.

- [ ] **Step 5: Commit Harness provider override**

Run:

```bash
gofmt -w internal/harness/harness.go internal/harness/harness_test.go
go test ./internal/harness
```

Expected: PASS.

Then commit:

```bash
git add internal/harness/harness.go internal/harness/harness_test.go
git commit -m "feat: allow harness provider override"
```

---

### Task 2: Add CLI smoke flags

**Files:**
- Modify: `cmd/cli/main_test.go`
- Modify: `cmd/cli/main.go`

- [ ] **Step 1: Write failing CLI option parser tests**

Append these tests after `TestReadInputRejectsEmptyOrWhitespaceInput` in `cmd/cli/main_test.go`:

```go
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
```

- [ ] **Step 2: Run CLI parser tests to verify failure**

Run:

```bash
go test ./cmd/cli -run 'TestParseCLIOptionsDefaults|TestParseCLIOptionsAcceptsSmokeFlags' -v
```

Expected: FAIL because `parseCLIOptions`, `defaultCLIProviderConfigPath`, and `cliOptions` are not defined.

- [ ] **Step 3: Implement CLI option parsing**

In `cmd/cli/main.go`, add `flag` to the import block:

```go
import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/harness"
	"harukizmoe/pimoe/internal/logger"
)
```

Replace the single `agentLogPath` const with:

```go
const (
	agentLogPath                 = ".moe/logs/agent.log"
	defaultCLIProviderConfigPath = "configs/providers.yaml"
)
```

Add this type and parser after `main` or before `readInput`:

```go
type cliOptions struct {
	configPath   string
	providerName string
	includeTrace bool
	promptArgs   []string
}

func parseCLIOptions(args []string) (cliOptions, error) {
	opts := cliOptions{configPath: defaultCLIProviderConfigPath}
	flags := flag.NewFlagSet("pimoe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.configPath, "config", opts.configPath, "providers YAML config path")
	flags.StringVar(&opts.providerName, "provider", "", "provider instance name")
	flags.BoolVar(&opts.includeTrace, "trace", false, "print tool trace")

	if err := flags.Parse(args); err != nil {
		return cliOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	opts.promptArgs = flags.Args()
	return opts, nil
}
```

- [ ] **Step 4: Route CLI options through Harness**

In `main`, replace:

```go
	input, err := readInput(os.Args[1:], os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	runner, err := harness.New(context.Background(), harness.Config{
		ProviderConfigPath: "configs/providers.yaml",
		Logger:             appLogger,
	})
```

with:

```go
	opts, err := parseCLIOptions(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	input, err := readInput(opts.promptArgs, os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	runner, err := harness.New(context.Background(), harness.Config{
		ProviderConfigPath: opts.configPath,
		ProviderName:       opts.providerName,
		Logger:             appLogger,
	})
```

Replace:

```go
	fmt.Print(formatRunResult(result, false))
```

with:

```go
	fmt.Print(formatRunResult(result, opts.includeTrace))
```

- [ ] **Step 5: Run CLI tests to verify pass**

Run:

```bash
go test ./cmd/cli -run 'TestParseCLIOptionsDefaults|TestParseCLIOptionsAcceptsSmokeFlags|TestReadInput|TestFormatRunResult' -v
```

Expected: PASS.

- [ ] **Step 6: Commit CLI smoke flags**

Run:

```bash
gofmt -w cmd/cli/main.go cmd/cli/main_test.go
go test ./cmd/cli
```

Expected: PASS.

Then commit:

```bash
git add cmd/cli/main.go cmd/cli/main_test.go
git commit -m "feat: add cli smoke flags"
```

---

### Task 3: Verify Harness-first smoke path

**Files:**
- Modify only touched implementation/test files if verification exposes an issue.

- [ ] **Step 1: Run changed package tests**

Run:

```bash
go test ./cmd/cli ./internal/harness ./internal/llms
```

Expected: PASS.

- [ ] **Step 2: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run go vet**

Run:

```bash
go vet ./...
```

Expected: no output and exit code 0.

- [ ] **Step 4: Check whether golangci-lint is available**

Run:

```bash
command -v golangci-lint
```

Expected when not installed: non-zero exit code and no lint run. Expected when installed: path printed.

If installed, run:

```bash
golangci-lint run
```

Expected: PASS.

- [ ] **Step 5: Run fake provider CLI smoke**

Run:

```bash
go run ./cmd/cli --provider fake --trace "use calculator to compute 13 * 7"
```

Expected output contains all of these strings:

```text
13 * 7 = 91
tool=calculator
arguments={"a":13,"b":7,"op":"mul"}
result=91
```

- [ ] **Step 6: Inspect final status**

Run:

```bash
git status --short
```

Expected: no unstaged or staged implementation/test changes. The existing untracked `configs/` directory may remain untracked if it predates this plan. If any implementation or test file is modified during verification, return to Task 1 or Task 2, add an exact failing test for the issue, fix it there, and rerun Task 3.
