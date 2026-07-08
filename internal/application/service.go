package application

import (
	"context"
	"fmt"
	"strings"

	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/session"
)

// Config 保存应用层服务需要的本地 session 和 Provider 配置。
type Config struct {
	// SessionRoot 是 manager-managed session index 和 JSONL transcript 的根目录。
	SessionRoot string
	// ProviderConfigPath 指向 providers YAML 配置文件。
	ProviderConfigPath string
	// ProviderName 选择配置文件中的 Provider 实例；为空时使用 default_provider。
	ProviderName string
	// Logger 接收 Agent 运行日志；为空时使用 no-op logger。
	Logger logger.Logger
}

// Service 是 HTTP、CLI 等入口复用的应用层门面。
type Service struct {
	manager *session.Manager
	config  session.Config
}

// SessionMeta 描述一个可由应用层返回给调用方的本地 session。
type SessionMeta = session.SessionMeta

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

// NewService 创建复用 internal/session 的应用层服务。
func NewService(cfg Config) (*Service, error) {
	if strings.TrimSpace(cfg.SessionRoot) == "" {
		return nil, fmt.Errorf("session root must not be empty")
	}
	if strings.TrimSpace(cfg.ProviderConfigPath) == "" {
		return nil, fmt.Errorf("provider config path must not be empty")
	}
	return &Service{
		manager: session.NewManager(cfg.SessionRoot),
		config: session.Config{
			ProviderConfigPath: cfg.ProviderConfigPath,
			ProviderName:       cfg.ProviderName,
			Logger:             cfg.Logger,
		},
	}, nil
}

// CreateSession 创建一个 manager-managed session，并用 title 生成可读标题。
func (s *Service) CreateSession(ctx context.Context, title string) (SessionMeta, error) {
	if s == nil {
		return SessionMeta{}, fmt.Errorf("application service is nil")
	}
	return s.manager.Create(ctx, title)
}

// ListSessions 按最近更新时间倒序返回本地 sessions。
func (s *Service) ListSessions(ctx context.Context) ([]SessionMeta, error) {
	if s == nil {
		return nil, fmt.Errorf("application service is nil")
	}
	return s.manager.List(ctx)
}

// Run 在指定 session 上执行一轮 prompt，并返回最终答案和工具步骤。
func (s *Service) Run(ctx context.Context, sessionID string, input string) (RunResult, error) {
	if s == nil {
		return RunResult{}, fmt.Errorf("application service is nil")
	}
	if strings.TrimSpace(sessionID) == "" {
		return RunResult{}, fmt.Errorf("session id must not be empty")
	}
	if strings.TrimSpace(input) == "" {
		return RunResult{}, fmt.Errorf("input must not be empty")
	}
	meta, err := s.manager.Resolve(ctx, sessionID)
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
	if err := s.manager.Touch(ctx, sessionID); err != nil {
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
