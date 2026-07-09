package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	appconfig "harukizmoe/pimoe/internal/config"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/session"
)

const (
	agentLogPath                 = ".moe/logs/agent.log"
	defaultCLIProviderConfigPath = "configs/providers.yaml"
	defaultCLISessionRoot        = ".moe/sessions"
)

func main() {
	ctx := context.Background()
	opts, err := parseCLIOptions(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	if opts.listSessions {
		if err := runListSessions(ctx, os.Stdout, defaultCLISessionRoot); err != nil {
			log.Fatal(err)
		}
		return
	}

	appLogger, closeLogger, err := logger.NewDevelopmentFile(agentLogPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := closeLogger(); err != nil {
			log.Printf("close logger: %v", err)
		}
	}()

	if opts.interactive {
		runner, managed, err := newCLISessionWithRoot(ctx, opts, appLogger, defaultCLISessionRoot, "interactive session")
		if err != nil {
			log.Fatal(err)
		}
		if err := runInteractive(ctx, runner, os.Stdin, os.Stdout, opts.includeTrace); err != nil {
			log.Fatal(err)
		}
		if err := touchManagedSession(ctx, managed); err != nil {
			log.Fatal(err)
		}
		return
	}

	input, err := readInput(opts.promptArgs, os.Stdin)
	if err != nil {
		log.Fatal(err)
	}

	runner, managed, err := newCLISessionWithRoot(ctx, opts, appLogger, defaultCLISessionRoot, input)
	if err != nil {
		log.Fatal(err)
	}

	output, err := collectRunOutput(runner.Prompt(ctx, input))
	if err != nil {
		log.Fatal(err)
	}
	if err := touchManagedSession(ctx, managed); err != nil {
		log.Fatal(err)
	}

	fmt.Print(formatRunOutput(output, opts.includeTrace))
}

type cliOptions struct {
	// configPath 指向 providers YAML，默认使用项目内开发配置。
	configPath string
	// providerName 选择配置文件中的 Provider 实例；为空时使用 default_provider。
	providerName string
	// maxSteps 限制本次或 managed session 恢复后的 tool-calling 轮数；小于 1 表示使用默认/已存储值。
	maxSteps int
	// sessionPrompt 是本次或 managed session 恢复后的会话级指令；不会写入 transcript。
	sessionPrompt string
	// sessionPath 非空时启用 JSONL 会话恢复，空值保持一次性内存会话。
	sessionPath string
	// newSession 表示创建 manager-managed session 并用本轮 prompt 作为标题来源。
	newSession bool
	// resumeSessionID 是从 manager index 恢复的 session id。
	resumeSessionID string
	// listSessions 表示只列出 manager-managed sessions，不创建 Agent 或读取 Provider。
	listSessions bool
	// includeTrace 控制 CLI 是否输出 tool call 调试轨迹。
	includeTrace bool
	// promptArgs 保存 flag 解析后的剩余参数，会被拼接为本轮用户输入。
	promptArgs []string
	// interactive 表示复用同一 Session 逐行读取 prompt，直到 quit/exit/EOF。
	interactive bool
}

func parseCLIOptions(args []string) (cliOptions, error) {
	opts := cliOptions{configPath: defaultCLIProviderConfigPath}
	flags := flag.NewFlagSet("pimoe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.configPath, "config", opts.configPath, "providers YAML config path")
	flags.StringVar(&opts.providerName, "provider", "", "provider instance name")
	flags.IntVar(&opts.maxSteps, "max-steps", 0, "maximum tool-calling rounds for this run/session")
	flags.StringVar(&opts.sessionPrompt, "session-prompt", "", "session prompt for this run/session")
	flags.StringVar(&opts.sessionPath, "session", "", "session JSONL path")
	flags.BoolVar(&opts.includeTrace, "trace", false, "print tool trace")
	flags.BoolVar(&opts.interactive, "interactive", false, "read prompts line by line until quit or EOF")
	flags.BoolVar(&opts.newSession, "new-session", false, "create a managed session")
	flags.StringVar(&opts.resumeSessionID, "resume", "", "managed session id to resume")
	flags.BoolVar(&opts.listSessions, "list-sessions", false, "list managed sessions")

	if err := flags.Parse(args); err != nil {
		return cliOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	opts.promptArgs = flags.Args()
	if err := validateCLIOptions(opts); err != nil {
		return cliOptions{}, err
	}
	return opts, nil
}

func validateCLIOptions(opts cliOptions) error {
	hasManualSession := strings.TrimSpace(opts.sessionPath) != ""
	hasResume := strings.TrimSpace(opts.resumeSessionID) != ""

	if hasManualSession && opts.newSession {
		return fmt.Errorf("--session and --new-session are mutually exclusive")
	}
	if hasManualSession && hasResume {
		return fmt.Errorf("--session and --resume are mutually exclusive")
	}
	if opts.newSession && hasResume {
		return fmt.Errorf("--new-session and --resume are mutually exclusive")
	}
	if opts.maxSteps < 0 {
		return fmt.Errorf("--max-steps must not be negative")
	}
	if opts.interactive && len(opts.promptArgs) > 0 {
		return fmt.Errorf("--interactive cannot be combined with prompt args")
	}
	if opts.listSessions {
		if len(opts.promptArgs) > 0 || opts.interactive || hasManualSession || opts.newSession || hasResume || opts.maxSteps > 0 || strings.TrimSpace(opts.sessionPrompt) != "" {
			return fmt.Errorf("--list-sessions cannot be combined with prompt, --interactive, --session, --new-session, --resume, --max-steps, or --session-prompt")
		}
	}
	return nil
}

type cliManagedSession struct {
	manager               *session.Manager
	ID                    string
	providerOverride      string
	maxStepsOverride      int
	sessionPromptOverride string
}

// newCLISession 根据 session 选项创建内存、显式 JSONL 或 manager-managed Session。
func newCLISession(ctx context.Context, opts cliOptions, appLogger logger.Logger) (*session.Session, error) {
	runner, _, err := newCLISessionWithRoot(ctx, opts, appLogger, defaultCLISessionRoot, strings.Join(opts.promptArgs, " "))
	return runner, err
}

func newCLISessionWithRoot(ctx context.Context, opts cliOptions, appLogger logger.Logger, sessionRoot string, title string) (*session.Session, *cliManagedSession, error) {
	cfg := session.Config{
		ProviderConfigPath: opts.configPath,
		ProviderName:       opts.providerName,
		BaseSystemPrompt:   "",
		SessionPrompt:      strings.TrimSpace(opts.sessionPrompt),
		Logger:             appLogger,
		MaxSteps:           opts.maxSteps,
	}
	if strings.TrimSpace(opts.sessionPath) != "" {
		runner, err := session.Open(ctx, cfg, opts.sessionPath)
		return runner, nil, err
	}

	manager := session.NewManager(sessionRoot)
	if opts.newSession {
		createCfg, err := newCLIManagedSessionConfig(opts)
		if err != nil {
			return nil, nil, err
		}
		cfg.ProviderName = createCfg.ProviderName
		cfg.SessionPrompt = createCfg.SessionPrompt
		cfg.MaxSteps = createCfg.MaxSteps
		if _, err := session.New(ctx, cfg); err != nil {
			return nil, nil, err
		}
		meta, err := manager.Create(ctx, title, createCfg)
		if err != nil {
			return nil, nil, err
		}
		runner, err := session.Open(ctx, cfg, meta.Path)
		if err != nil {
			return nil, nil, err
		}
		return runner, &cliManagedSession{manager: manager, ID: meta.ID, providerOverride: opts.providerName, maxStepsOverride: opts.maxSteps, sessionPromptOverride: opts.sessionPrompt}, nil
	}

	if strings.TrimSpace(opts.resumeSessionID) != "" {
		meta, err := manager.Resolve(ctx, opts.resumeSessionID)
		if err != nil {
			return nil, nil, err
		}
		runProvider := strings.TrimSpace(opts.providerName)
		if runProvider == "" {
			runProvider = meta.Config.ProviderName
			if err := ensureCLIStoredProviderConfigured(opts.configPath, meta.ID, runProvider); err != nil {
				return nil, nil, err
			}
		}
		cfg.ProviderName = runProvider
		if opts.maxSteps > 0 {
			cfg.MaxSteps = opts.maxSteps
		} else {
			cfg.MaxSteps = meta.Config.MaxSteps
		}
		if sessionPrompt := strings.TrimSpace(opts.sessionPrompt); sessionPrompt != "" {
			cfg.SessionPrompt = sessionPrompt
		} else {
			cfg.SessionPrompt = meta.Config.SessionPrompt
		}
		runner, err := session.Open(ctx, cfg, meta.Path)
		if err != nil {
			return nil, nil, err
		}
		return runner, &cliManagedSession{manager: manager, ID: meta.ID, providerOverride: opts.providerName, maxStepsOverride: opts.maxSteps, sessionPromptOverride: opts.sessionPrompt}, nil
	}

	runner, err := session.New(ctx, cfg)
	return runner, nil, err
}

func newCLIManagedSessionConfig(opts cliOptions) (session.SessionConfig, error) {
	providerName := strings.TrimSpace(opts.providerName)
	cfg := session.SessionConfig{SessionPrompt: strings.TrimSpace(opts.sessionPrompt), MaxSteps: opts.maxSteps}
	if providerName != "" {
		cfg.ProviderName = providerName
		return cfg, nil
	}
	loaded, err := appconfig.Load(opts.configPath)
	if err != nil {
		return session.SessionConfig{}, err
	}
	cfg.ProviderName = strings.TrimSpace(loaded.LLMs.DefaultProvider)
	return cfg, nil
}

func ensureCLIStoredProviderConfigured(configPath, sessionID, providerName string) error {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil
	}
	loaded, err := appconfig.Load(configPath)
	if err != nil {
		return err
	}
	if _, ok := loaded.LLMs.Providers[providerName]; ok {
		return nil
	}
	return fmt.Errorf("session %q provider %q is not configured; specify --provider to choose another provider", sessionID, providerName)
}

func touchManagedSession(ctx context.Context, managed *cliManagedSession) error {
	if managed == nil {
		return nil
	}
	providerOverride := strings.TrimSpace(managed.providerOverride)
	sessionPromptOverride := strings.TrimSpace(managed.sessionPromptOverride)
	if providerOverride != "" || managed.maxStepsOverride > 0 || sessionPromptOverride != "" {
		meta, err := managed.manager.Resolve(ctx, managed.ID)
		if err != nil {
			return err
		}
		cfg := meta.Config
		if providerOverride != "" {
			cfg.ProviderName = providerOverride
		}
		if managed.maxStepsOverride > 0 {
			cfg.MaxSteps = managed.maxStepsOverride
		}
		if sessionPromptOverride != "" {
			cfg.SessionPrompt = sessionPromptOverride
		}
		return managed.manager.UpdateConfig(ctx, managed.ID, cfg)
	}
	return managed.manager.Touch(ctx, managed.ID)
}

func readInput(args []string, stdin io.Reader) (string, error) {
	input := strings.TrimSpace(strings.Join(args, " "))
	if input == "" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		input = strings.TrimSpace(string(data))
	}
	if input == "" {
		return "", fmt.Errorf("empty input")
	}
	return input, nil
}

const maxInteractivePromptBytes = 1024 * 1024

func runInteractive(ctx context.Context, runner *session.Session, input io.Reader, output io.Writer, includeTrace bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), maxInteractivePromptBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		if strings.EqualFold(prompt, "quit") || strings.EqualFold(prompt, "exit") {
			return nil
		}

		runOutput, err := collectRunOutput(runner.Prompt(ctx, prompt))
		if err != nil {
			return err
		}
		if _, err := io.WriteString(output, formatRunOutput(runOutput, includeTrace)); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read interactive input: %w", err)
	}
	return nil
}

type runOutput struct {
	Answer string
	Steps  []toolStep
}

type toolStep struct {
	ToolCallID string
	ToolName   string
	Arguments  string
	Result     string
	Error      string
}

func collectRunOutput(events <-chan session.Event) (runOutput, error) {
	var output runOutput
	stepByCallID := make(map[string]int)

	for event := range events {
		switch event := event.(type) {
		case session.ToolExecutionStartEvent:
			stepByCallID[event.ToolCallID] = len(output.Steps)
			output.Steps = append(output.Steps, toolStep{
				ToolCallID: event.ToolCallID,
				ToolName:   event.ToolName,
				Arguments:  event.Arguments,
			})
		case session.ToolExecutionEndEvent:
			stepIndex, ok := stepByCallID[event.ToolCallID]
			if !ok {
				stepIndex = len(output.Steps)
				stepByCallID[event.ToolCallID] = stepIndex
				output.Steps = append(output.Steps, toolStep{ToolCallID: event.ToolCallID, ToolName: event.Result.ToolName})
			}
			if event.Error != nil {
				output.Steps[stepIndex].Error = event.Error.Error()
			} else if event.Result.IsError {
				output.Steps[stepIndex].Error = event.Result.Content
			} else {
				output.Steps[stepIndex].Result = event.Result.Content
			}
		case session.MessageEndEvent:
			if len(event.Message.ToolCalls) == 0 {
				output.Answer = event.Message.Content
			}
		case session.ErrorEvent:
			if event.Error != nil {
				return output, event.Error
			}
		}
	}

	return output, nil
}

func runListSessions(ctx context.Context, output io.Writer, root string) error {
	metas, err := session.NewManager(root).List(ctx)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(output, formatSessionListOutput(metas)); err != nil {
		return fmt.Errorf("write sessions: %w", err)
	}
	return nil
}

func formatSessionListOutput(metas []session.SessionMeta) string {
	var builder strings.Builder
	for _, meta := range metas {
		builder.WriteString(meta.ID)
		builder.WriteString("  ")
		builder.WriteString(meta.UpdatedAt.UTC().Format(time.RFC3339))
		builder.WriteString("  ")
		builder.WriteString(meta.Title)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func formatRunOutput(output runOutput, includeTrace bool) string {
	var builder strings.Builder
	builder.WriteString(output.Answer)
	builder.WriteByte('\n')

	if !includeTrace {
		return builder.String()
	}

	for _, step := range output.Steps {
		builder.WriteString("\n")
		builder.WriteString("tool=")
		builder.WriteString(step.ToolName)
		builder.WriteByte('\n')
		builder.WriteString("arguments=")
		builder.WriteString(step.Arguments)
		builder.WriteByte('\n')
		if step.Error != "" {
			builder.WriteString("error=")
			builder.WriteString(step.Error)
			builder.WriteByte('\n')
			continue
		}
		builder.WriteString("result=")
		builder.WriteString(step.Result)
		builder.WriteByte('\n')
	}

	return builder.String()
}
