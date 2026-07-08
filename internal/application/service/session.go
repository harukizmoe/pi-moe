package service

import (
	"context"
	"fmt"
	"strings"

	"harukizmoe/pimoe/internal/application/data"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/session"
)

// SessionConfig 保存创建 SessionService 所需的依赖和 Provider 配置。
type SessionConfig struct {
	// Store 是 session metadata 的数据层接口。
	Store data.SessionStore
	// ProviderConfigPath 指向 providers YAML 配置文件。
	ProviderConfigPath string
	// ProviderName 选择配置文件中的 Provider 实例；为空时使用 default_provider。
	ProviderName string
	// Logger 接收 Agent 运行日志；为空时使用 no-op logger。
	Logger logger.Logger
}

// SessionService 编排 session metadata、transcript 和 Agent run。
type SessionService struct {
	store  data.SessionStore
	config session.Config
}

// SessionMeta 描述一个可由应用层返回给调用方的本地 session。
type SessionMeta = data.SessionMeta

// RunResult 保存一次 prompt 运行的最终答案和工具步骤。
type RunResult struct {
	// Answer 是 assistant 最终非 tool-call 回复。
	Answer string
	// ToolSteps 保存本轮运行发生的工具调用及其结果。
	ToolSteps []ToolStep
}

// ToolStep 描述一次工具调用的输入和结果。
type ToolStep struct {
	// ToolCallID 是模型生成的 tool call id。
	ToolCallID string
	// ToolName 是本地工具名。
	ToolName string
	// Arguments 是模型传给工具的原始 JSON 参数。
	Arguments string
	// Result 是工具成功执行后的文本结果。
	Result string
	// Error 是工具失败时的错误摘要。
	Error string
}

// NewSessionService 创建 session 业务服务。
func NewSessionService(cfg SessionConfig) (*SessionService, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("session store must not be nil")
	}
	if strings.TrimSpace(cfg.ProviderConfigPath) == "" {
		return nil, fmt.Errorf("provider config path must not be empty")
	}
	return &SessionService{
		store: cfg.Store,
		config: session.Config{
			ProviderConfigPath: cfg.ProviderConfigPath,
			ProviderName:       cfg.ProviderName,
			Logger:             cfg.Logger,
		},
	}, nil
}

// Create 创建一个 managed session，并用 title 生成可读标题。
func (s *SessionService) Create(ctx context.Context, title string) (SessionMeta, error) {
	if s == nil {
		return SessionMeta{}, fmt.Errorf("session service is nil")
	}
	return s.store.Create(ctx, title)
}

// List 返回可恢复 sessions。
func (s *SessionService) List(ctx context.Context) ([]SessionMeta, error) {
	if s == nil {
		return nil, fmt.Errorf("session service is nil")
	}
	return s.store.List(ctx)
}

// Run 在指定 session 上执行一轮 prompt，并返回最终答案和工具步骤。
func (s *SessionService) Run(ctx context.Context, sessionID string, input string) (RunResult, error) {
	if s == nil {
		return RunResult{}, fmt.Errorf("session service is nil")
	}
	if strings.TrimSpace(input) == "" {
		return RunResult{}, fmt.Errorf("input must not be empty")
	}
	meta, err := s.store.Resolve(ctx, sessionID)
	if err != nil {
		return RunResult{}, err
	}
	runner, err := session.Open(ctx, s.config, meta.Path)
	if err != nil {
		return RunResult{}, err
	}
	result, err := collectRunResult(runner.Prompt(ctx, input))
	if err != nil {
		return result, err
	}
	if err := s.store.Touch(ctx, sessionID); err != nil {
		return result, err
	}
	return result, nil
}

func collectRunResult(events <-chan session.Event) (RunResult, error) {
	var result RunResult
	stepByCallID := make(map[string]int)
	for event := range events {
		switch event := event.(type) {
		case session.ToolExecutionStartEvent:
			stepByCallID[event.ToolCallID] = len(result.ToolSteps)
			result.ToolSteps = append(result.ToolSteps, ToolStep{ToolCallID: event.ToolCallID, ToolName: event.ToolName, Arguments: event.Arguments})
		case session.ToolExecutionEndEvent:
			stepIndex, ok := stepByCallID[event.ToolCallID]
			if !ok {
				stepIndex = len(result.ToolSteps)
				stepByCallID[event.ToolCallID] = stepIndex
				result.ToolSteps = append(result.ToolSteps, ToolStep{ToolCallID: event.ToolCallID, ToolName: event.Result.ToolName})
			}
			if event.Error != nil {
				result.ToolSteps[stepIndex].Error = event.Error.Error()
			} else if event.Result.IsError {
				result.ToolSteps[stepIndex].Error = event.Result.Content
			} else {
				result.ToolSteps[stepIndex].Result = event.Result.Content
			}
		case session.MessageEndEvent:
			if len(event.Message.ToolCalls) == 0 {
				result.Answer = event.Message.Content
			}
		case session.ErrorEvent:
			if event.Error != nil {
				return result, event.Error
			}
		}
	}
	return result, nil
}
