package harness

import (
	"context"
	"fmt"
	"strings"

	"harukizmoe/pimoe/internal/agent"
	appconfig "harukizmoe/pimoe/internal/config"
	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/tools"
)

const defaultProviderConfigPath = "configs/providers.yaml"

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
}

// Harness 是后端入口可复用的 Agent 运行内核。
type Harness struct {
	agent *agent.Agent
}

// New 从配置文件组装 Provider、工具注册表和 Agent。
func New(ctx context.Context, cfg Config) (*Harness, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("create harness: %w", err)
		}
	}

	path := cfg.ProviderConfigPath
	if path == "" {
		path = defaultProviderConfigPath
	}

	loaded, err := appconfig.Load(path)
	if err != nil {
		return nil, err
	}

	providerName := cfg.ProviderName
	if providerName == "" {
		providerName = loaded.LLMs.DefaultProvider
	}
	providerConfig, ok := loaded.LLMs.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", providerName)
	}

	llmRegistry := llms.NewRegistry()
	llmRegistry.Register("fake", llms.NewFakeProvider)
	llmRegistry.Register("openai_compatible", llms.NewOpenAICompatibleProvider)

	provider, err := llmRegistry.NewProvider(providerConfig)
	if err != nil {
		return nil, err
	}

	toolRegistry := tools.NewRegistry()
	toolRegistry.Register(tools.Calculator{})

	runner := agent.NewWithOptions(provider, toolRegistry, providerConfig.Model, agent.Options{
		Logger:   cfg.Logger,
		MaxSteps: cfg.MaxSteps,
	})

	return &Harness{agent: runner}, nil
}

// Stream 执行一次 Agent 运行，并通过 channel 实时返回运行事件。
func (h *Harness) Stream(ctx context.Context, input string) <-chan Event {
	stream := make(chan Event)
	go func() {
		defer close(stream)
		if strings.TrimSpace(input) == "" {
			emitHarnessError(ctx, stream, fmt.Errorf("empty input"))
			return
		}
		for event := range h.agent.Stream(ctx, []agent.Message{agent.UserMessage{Content: input}}) {
			if !emitHarnessEvent(ctx, stream, event) {
				return
			}
		}
	}()
	return stream
}

func emitHarnessError(ctx context.Context, stream chan<- Event, err error) bool {
	return emitHarnessEvent(ctx, stream, ErrorEvent{Error: err})
}

func emitHarnessEvent(ctx context.Context, stream chan<- Event, event Event) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return false
	}
	select {
	case stream <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// Run 执行一次 Agent 运行，并返回结构化结果。
func (h *Harness) Run(ctx context.Context, input string) (*agent.RunResult, error) {
	return h.RunAgentMessages(ctx, []agent.Message{agent.UserMessage{Content: input}})
}

// RunAgentMessages 从调用方提供的强语义无状态对话历史继续执行 Agent。
func (h *Harness) RunAgentMessages(ctx context.Context, messages []agent.Message) (*agent.RunResult, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("empty input")
	}

	lastMessage, ok := messages[len(messages)-1].(agent.UserMessage)
	if ok && strings.TrimSpace(lastMessage.Content) == "" {
		return nil, fmt.Errorf("empty input")
	}

	return collectRunResult(ctx, messages, h.agent.Stream(ctx, messages))
}

func collectRunResult(ctx context.Context, history []agent.Message, stream <-chan Event) (*agent.RunResult, error) {
	var (
		result      *agent.RunResult
		sawProgress bool
		activeSteps = make(map[string]int)
	)

	ensureResult := func() *agent.RunResult {
		if result == nil {
			result = &agent.RunResult{Messages: cloneSessionMessages(history)}
		}
		return result
	}

	for event := range stream {
		switch event := event.(type) {
		case RunStartEvent, TurnStartEvent, MessageStartEvent, MessageDeltaEvent, TurnEndEvent:
			sawProgress = true
			ensureResult()
		case MessageEndEvent:
			sawProgress = true
			runResult := ensureResult()
			runResult.Messages = append(runResult.Messages, cloneAssistantMessage(event.Message))
			if len(event.Message.ToolCalls) == 0 {
				runResult.Answer = event.Message.Content
			} else {
				runResult.ToolRounds++
			}
		case ToolExecutionStartEvent:
			sawProgress = true
			runResult := ensureResult()
			runResult.Steps = append(runResult.Steps, agent.Step{
				ToolCallID: event.ToolCallID,
				ToolName:   event.ToolName,
				Arguments:  event.Arguments,
			})
			activeSteps[event.ToolCallID] = len(runResult.Steps) - 1
		case ToolExecutionEndEvent:
			sawProgress = true
			runResult := ensureResult()
			runResult.Messages = append(runResult.Messages, event.Result)
			updateRunStep(runResult, activeSteps, event)
		case RunEndEvent:
			ensureResult()
			return result, nil
		case ErrorEvent:
			if !sawProgress {
				return nil, event.Error
			}
			return ensureResult(), event.Error
		}
	}

	if ctx != nil && ctx.Err() != nil {
		if !sawProgress {
			return nil, ctx.Err()
		}
		return ensureResult(), ctx.Err()
	}
	if result != nil {
		return result, nil
	}
	return nil, nil
}

func updateRunStep(result *agent.RunResult, activeSteps map[string]int, event ToolExecutionEndEvent) {
	index, ok := activeSteps[event.ToolCallID]
	if !ok {
		result.Steps = append(result.Steps, agent.Step{
			ToolCallID: event.ToolCallID,
			ToolName:   event.Result.ToolName,
		})
		index = len(result.Steps) - 1
	}

	step := &result.Steps[index]
	if step.ToolName == "" {
		step.ToolName = event.Result.ToolName
	}
	if event.Error != nil {
		step.Error = event.Error.Error()
		step.Result = ""
	} else {
		step.Result = event.Result.Content
		step.Error = ""
	}
	delete(activeSteps, event.ToolCallID)
}
