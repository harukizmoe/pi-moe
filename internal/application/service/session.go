package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/application/data"
	appconfig "harukizmoe/pimoe/internal/config"
	"harukizmoe/pimoe/internal/llms"
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
	// SystemPrompt 保存新 session 的行为设定，并在运行时作为系统级指令注入 Agent。
	SystemPrompt string
	// MaxSteps 限制一次运行最多执行多少轮 tool calling；小于 1 时使用 Agent 默认值。
	MaxSteps int
	// Logger 接收 Agent 运行日志；为空时使用 no-op logger。
	Logger logger.Logger
}

// SessionService 编排 session metadata、transcript 和 Agent run。
type SessionService struct {
	store        data.SessionStore
	config       session.Config
	systemPrompt string
}

// SessionMeta 描述一个可由应用层返回给调用方的本地 session。
type SessionMeta = data.SessionMeta

// SessionDetail 描述一个 session 的 metadata 和已持久化 transcript。
type SessionDetail struct {
	SessionMeta
	// Messages 是按执行顺序恢复的 terminal transcript。
	Messages []SessionMessage
}

// SessionMessage 是对外稳定暴露的 transcript message DTO。
type SessionMessage struct {
	// Role 是 user、assistant 或 tool。
	Role string
	// Content 是 message 的可见文本或工具结果。
	Content string
	// ToolCalls 保存 assistant 请求执行的工具调用。
	ToolCalls []SessionToolCall
	// ToolCallID 将 tool result 关联到 assistant tool call。
	ToolCallID string
	// Tool 是 tool result 对应的本地工具名。
	Tool string
}

// SessionToolCall 是 assistant message 中的工具调用。
type SessionToolCall struct {
	// ID 是模型生成的 tool call id。
	ID string
	// Tool 是本地工具名。
	Tool string
	// Arguments 是模型传给工具的原始 JSON 参数。
	Arguments json.RawMessage
}

// RunResult 保存一次 prompt 运行的最终答案和工具步骤。
type RunResult struct {
	// Answer 是 assistant 最终非 tool-call 回复。
	Answer string
	// ToolSteps 保存本轮运行发生的工具调用及其结果。
	ToolSteps []ToolStep
}

// RunOptions 保存单次运行可覆盖的偏好。
type RunOptions struct {
	// ProviderName 覆盖本次运行使用的 Provider；成功后写回 session 偏好。
	ProviderName string
}

// CreateOptions 保存创建 session 时可持久化的运行偏好覆盖。
type CreateOptions struct {
	// ProviderName 覆盖新 session 使用的 Provider；为空时使用服务配置或 llms.default_provider。
	ProviderName string
	// SystemPrompt 覆盖新 session 的行为设定；为空时使用服务配置。
	SystemPrompt string
	// MaxSteps 覆盖新 session 的 tool-calling 最大轮数；小于 1 时使用服务配置。
	MaxSteps int
}

type providerSelectionError struct {
	err error
}

func (e providerSelectionError) Error() string {
	return e.err.Error()
}

func (e providerSelectionError) Unwrap() error {
	return e.err
}

// IsProviderSelectionError reports whether err was caused by choosing an unavailable provider.
func IsProviderSelectionError(err error) bool {
	var providerErr providerSelectionError
	return errors.As(err, &providerErr)
}

func newProviderSelectionError(format string, args ...any) error {
	return providerSelectionError{err: fmt.Errorf(format, args...)}
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

// StreamEvent 描述对 HTTP SSE 稳定暴露的应用层事件。
type StreamEvent struct {
	// Name 是 SSE event 名称。
	Name string
	// Data 是可 JSON 序列化的事件数据。
	Data any
}

// ProviderDiagnostics 描述当前 HTTP 服务选中的 Provider 配置健康状态。
type ProviderDiagnostics struct {
	// Name 是配置文件中的 Provider 实例名。
	Name string `json:"name"`
	// Type 是 Provider 实现类型，例如 fake 或 openai_compatible。
	Type string `json:"type"`
	// Model 是请求 Provider 时使用的模型名。
	Model string `json:"model"`
	// Ready 表示本地配置是否足以发起 Provider 调用。
	Ready bool `json:"ready"`
	// Error 是不可用原因；Ready 为 true 时为空。
	Error string `json:"error"`
}

type streamDeltaData struct {
	Content string `json:"content"`
}

type streamToolCallData struct {
	ID        string          `json:"id"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type streamToolResultData struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

type streamDoneData struct {
	Answer string `json:"answer"`
}

type streamErrorData struct {
	Error string `json:"error"`
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
			BaseSystemPrompt:   strings.TrimSpace(cfg.SystemPrompt),
			Logger:             cfg.Logger,
			MaxSteps:           cfg.MaxSteps,
		},
		systemPrompt: strings.TrimSpace(cfg.SystemPrompt),
	}, nil
}

// CurrentProviderDiagnostics 返回当前选中 Provider 的本地配置健康状态。
func (s *SessionService) CurrentProviderDiagnostics(ctx context.Context) (ProviderDiagnostics, error) {
	if s == nil {
		return ProviderDiagnostics{}, fmt.Errorf("session service is nil")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return ProviderDiagnostics{}, err
		}
	}

	loaded, err := appconfig.Load(s.config.ProviderConfigPath)
	if err != nil {
		return ProviderDiagnostics{}, err
	}
	providerName := s.config.ProviderName
	if providerName == "" {
		providerName = loaded.LLMs.DefaultProvider
	}
	providerConfig, ok := loaded.LLMs.Providers[providerName]
	if !ok {
		return ProviderDiagnostics{}, fmt.Errorf("unknown provider %q", providerName)
	}

	diagnostics := ProviderDiagnostics{
		Name:  providerName,
		Type:  providerConfig.Type,
		Model: providerConfig.Model,
		Ready: true,
	}
	switch providerConfig.Type {
	case "fake":
	case "openai_compatible":
		if strings.TrimSpace(providerConfig.BaseURL) == "" {
			diagnostics.Ready = false
			diagnostics.Error = "openai-compatible base_url is required"
			break
		}
		if strings.TrimSpace(providerConfig.APIKey) == "" {
			diagnostics.Ready = false
			if strings.TrimSpace(providerConfig.APIKeyEnv) == "" {
				diagnostics.Error = "api_key_env is not configured"
			} else {
				diagnostics.Error = fmt.Sprintf("environment variable %s is not set", providerConfig.APIKeyEnv)
			}
		}
	default:
		diagnostics.Ready = false
		diagnostics.Error = fmt.Sprintf("unknown llm provider type %q", providerConfig.Type)
	}
	return diagnostics, nil
}

func (s *SessionService) defaultSessionConfig(ctx context.Context, opts CreateOptions) (session.SessionConfig, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return session.SessionConfig{}, err
		}
	}
	cfg := session.SessionConfig{
		ProviderName:  strings.TrimSpace(s.config.ProviderName),
		SessionPrompt: strings.TrimSpace(s.systemPrompt),
		MaxSteps:      s.config.MaxSteps,
	}
	if providerName := strings.TrimSpace(opts.ProviderName); providerName != "" {
		cfg.ProviderName = providerName
	} else if cfg.ProviderName == "" {
		loaded, err := appconfig.Load(s.config.ProviderConfigPath)
		if err != nil {
			return session.SessionConfig{}, err
		}
		cfg.ProviderName = strings.TrimSpace(loaded.LLMs.DefaultProvider)
	}
	if systemPrompt := strings.TrimSpace(opts.SystemPrompt); systemPrompt != "" {
		cfg.SessionPrompt = systemPrompt
	}
	if opts.MaxSteps > 0 {
		cfg.MaxSteps = opts.MaxSteps
	}
	return cfg, nil
}

func firstRunOptions(opts []RunOptions) RunOptions {
	if len(opts) == 0 {
		return RunOptions{}
	}
	return opts[0]
}

func firstCreateOptions(opts []CreateOptions) CreateOptions {
	if len(opts) == 0 {
		return CreateOptions{}
	}
	return opts[0]
}

func (s *SessionService) resolveRunConfig(ctx context.Context, meta SessionMeta, opts RunOptions) (session.Config, string, bool, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return session.Config{}, "", false, err
		}
	}
	providerName := strings.TrimSpace(opts.ProviderName)
	override := providerName != ""
	if providerName == "" {
		providerName = strings.TrimSpace(meta.Config.ProviderName)
	}
	if providerName == "" {
		providerName = strings.TrimSpace(s.config.ProviderName)
	}

	runCfg := s.config
	runCfg.ProviderName = providerName
	if meta.Config.MaxSteps > 0 {
		runCfg.MaxSteps = meta.Config.MaxSteps
	}
	if systemPrompt := strings.TrimSpace(meta.Config.SessionPrompt); systemPrompt != "" && systemPrompt != strings.TrimSpace(runCfg.BaseSystemPrompt) {
		runCfg.SessionPrompt = systemPrompt
	}
	if err := ensureProviderConfigured(runCfg.ProviderConfigPath, providerName); err != nil {
		if meta.Config.ProviderName != "" && !override {
			return session.Config{}, "", false, newProviderSelectionError("session %q provider %q is not configured; specify provider_name to choose another provider", meta.ID, providerName)
		}
		return session.Config{}, "", false, err
	}
	return runCfg, providerName, override, nil
}

func ensureProviderConfigured(path string, providerName string) error {
	if strings.TrimSpace(providerName) == "" {
		return nil
	}
	loaded, err := appconfig.Load(path)
	if err != nil {
		return err
	}
	if _, ok := loaded.LLMs.Providers[providerName]; !ok {
		return newProviderSelectionError("unknown provider %q", providerName)
	}
	return nil
}

func (s *SessionService) persistRunSuccess(ctx context.Context, meta SessionMeta, providerName string, override bool) error {
	if override {
		cfg := meta.Config
		cfg.ProviderName = providerName
		return s.store.UpdateConfig(ctx, meta.ID, cfg)
	}
	return s.store.Touch(ctx, meta.ID)
}

// Create 创建一个 managed session，并用 title 生成可读标题。
func (s *SessionService) Create(ctx context.Context, title string, opts ...CreateOptions) (SessionMeta, error) {
	if s == nil {
		return SessionMeta{}, fmt.Errorf("session service is nil")
	}
	cfg, err := s.defaultSessionConfig(ctx, firstCreateOptions(opts))
	if err != nil {
		return SessionMeta{}, err
	}
	return s.store.Create(ctx, title, cfg)
}

// List 返回可恢复 sessions。
func (s *SessionService) List(ctx context.Context) ([]SessionMeta, error) {
	if s == nil {
		return nil, fmt.Errorf("session service is nil")
	}
	return s.store.List(ctx)
}

// Get 返回 session metadata 和当前可恢复 transcript。
func (s *SessionService) Get(ctx context.Context, sessionID string) (SessionDetail, error) {
	if s == nil {
		return SessionDetail{}, fmt.Errorf("session service is nil")
	}
	meta, err := s.store.Resolve(ctx, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}
	loaded, err := session.LoadMessages(meta.Path)
	if err != nil {
		return SessionDetail{}, err
	}
	messages, err := newSessionMessages(loaded)
	if err != nil {
		return SessionDetail{}, err
	}
	return SessionDetail{SessionMeta: meta, Messages: messages}, nil
}

// Run 在指定 session 上执行一轮 prompt，并返回最终答案和工具步骤。
func (s *SessionService) Run(ctx context.Context, sessionID string, input string, opts ...RunOptions) (RunResult, error) {
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
	runCfg, providerName, override, err := s.resolveRunConfig(ctx, meta, firstRunOptions(opts))
	if err != nil {
		return RunResult{}, err
	}
	runner, err := session.Open(ctx, runCfg, meta.Path)
	if err != nil {
		return RunResult{}, err
	}
	result, err := collectRunResult(runner.Prompt(ctx, input))
	if err != nil {
		return result, err
	}
	if err := s.persistRunSuccess(ctx, meta, providerName, override); err != nil {
		return result, err
	}
	return result, nil
}

// Stream 在指定 session 上执行一轮 prompt，并返回稳定的应用层流式事件。
func (s *SessionService) Stream(ctx context.Context, sessionID string, input string, opts ...RunOptions) (<-chan StreamEvent, error) {
	if s == nil {
		return nil, fmt.Errorf("session service is nil")
	}
	if strings.TrimSpace(input) == "" {
		return nil, fmt.Errorf("input must not be empty")
	}
	meta, err := s.store.Resolve(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	runCfg, providerName, override, err := s.resolveRunConfig(ctx, meta, firstRunOptions(opts))
	if err != nil {
		return nil, err
	}
	runner, err := session.Open(ctx, runCfg, meta.Path)
	if err != nil {
		return nil, err
	}

	out := make(chan StreamEvent)
	go s.forwardStreamEvents(ctx, meta, providerName, override, runner.Prompt(ctx, input), out)
	return out, nil
}

func newSessionMessages(messages []agent.Message) ([]SessionMessage, error) {
	out := make([]SessionMessage, 0, len(messages))
	for _, message := range messages {
		converted, err := newSessionMessage(message)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func newSessionMessage(message agent.Message) (SessionMessage, error) {
	switch msg := message.(type) {
	case agent.UserMessage:
		return SessionMessage{Role: string(llms.RoleUser), Content: msg.Content}, nil
	case agent.AssistantMessage:
		return SessionMessage{Role: string(llms.RoleAssistant), Content: msg.Content, ToolCalls: newSessionToolCalls(msg.ToolCalls)}, nil
	case agent.ToolResultMessage:
		return SessionMessage{Role: string(llms.RoleTool), ToolCallID: msg.ToolCallID, Tool: msg.ToolName, Content: msg.Content}, nil
	default:
		return SessionMessage{}, fmt.Errorf("unsupported session message type %T", message)
	}
}

func newSessionToolCalls(calls []llms.ToolCall) []SessionToolCall {
	out := make([]SessionToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, SessionToolCall{ID: call.ID, Tool: call.Function.Name, Arguments: stableRawJSON(call.Function.Arguments)})
	}
	return out
}

func stableRawJSON(value string) json.RawMessage {
	if json.Valid([]byte(value)) {
		return json.RawMessage(value)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

func (s *SessionService) forwardStreamEvents(ctx context.Context, meta SessionMeta, providerName string, override bool, events <-chan session.Event, out chan<- StreamEvent) {
	defer close(out)
	var answer string
	for event := range events {
		switch event := event.(type) {
		case session.MessageDeltaEvent:
			if event.Kind == session.MessageDeltaText && event.Delta != "" {
				answer += event.Delta
				if !sendStreamEvent(ctx, out, StreamEvent{Name: "delta", Data: streamDeltaData{Content: event.Delta}}) {
					return
				}
			}
		case session.ToolExecutionStartEvent:
			if !sendStreamEvent(ctx, out, StreamEvent{Name: "tool_call", Data: streamToolCallData{ID: event.ToolCallID, Tool: event.ToolName, Arguments: json.RawMessage(event.Arguments)}}) {
				return
			}
		case session.ToolExecutionEndEvent:
			data := streamToolResultData{ID: event.ToolCallID, Tool: event.Result.ToolName}
			if event.Error != nil {
				data.Error = event.Error.Error()
			} else if event.Result.IsError {
				data.Error = event.Result.Content
			} else {
				data.Result = event.Result.Content
			}
			if !sendStreamEvent(ctx, out, StreamEvent{Name: "tool_result", Data: data}) {
				return
			}
		case session.MessageEndEvent:
			if len(event.Message.ToolCalls) == 0 {
				answer = event.Message.Content
			}
		case session.RunEndEvent:
			if err := s.persistRunSuccess(ctx, meta, providerName, override); err != nil {
				_ = sendStreamEvent(ctx, out, StreamEvent{Name: "error", Data: streamErrorData{Error: err.Error()}})
				return
			}
			_ = sendStreamEvent(ctx, out, StreamEvent{Name: "done", Data: streamDoneData{Answer: answer}})
			return
		case session.ErrorEvent:
			if event.Error != nil {
				_ = sendStreamEvent(ctx, out, StreamEvent{Name: "error", Data: streamErrorData{Error: event.Error.Error()}})
			}
			return
		}
	}
}

func sendStreamEvent(ctx context.Context, out chan<- StreamEvent, event StreamEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
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
