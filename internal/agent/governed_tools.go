package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

// ToolResultStatus 标识一次 tool call 的稳定终态。
type ToolResultStatus string

const (
	// ToolResultSuccess 表示 executor 已成功返回安全模型结果。
	ToolResultSuccess ToolResultStatus = "success"
	// ToolResultError 表示参数校验或 executor 执行失败。
	ToolResultError ToolResultStatus = "error"
	// ToolResultDenied 表示工具未授权或审批未通过。
	ToolResultDenied ToolResultStatus = "denied"
	// ToolResultTimeout 表示 Tool timeout 先于 Run deadline 到期。
	ToolResultTimeout ToolResultStatus = "timeout"
	// ToolResultCanceled 表示 Run context 在执行或审批期间被取消。
	ToolResultCanceled ToolResultStatus = "canceled"
	// ToolResultSkipped 表示未知工具或批处理中因先前取消而未执行。
	ToolResultSkipped ToolResultStatus = "skipped"
)

// ToolConcurrencyPolicy 描述工具声明的并发安全能力；v1 执行器仍默认串行。
type ToolConcurrencyPolicy string

const (
	// ToolConcurrencyExclusive 是默认策略，禁止与其他 tool call 并发执行。
	ToolConcurrencyExclusive ToolConcurrencyPolicy = "exclusive"
	// ToolConcurrencyShared 仅允许显式声明为只读且线程安全的工具使用。
	ToolConcurrencyShared ToolConcurrencyPolicy = "shared"
)

// ToolPolicy 描述 Runtime 能统一执行的工具治理策略。
type ToolPolicy struct {
	// Timeout 限制单次 executor 调用；零表示只受 Run context 控制。
	Timeout time.Duration
	// RequiresApproval 表示执行前必须通过 request-scoped ApprovalGate。
	RequiresApproval bool
	// ReadOnly 表示 executor 不产生业务副作用。
	ReadOnly bool
	// Concurrency 声明 exclusive 或 shared；空值按 exclusive 处理。
	Concurrency ToolConcurrencyPolicy
}

// ToolExecutionRequest 是传给已授权 executor 的最小调用数据。
type ToolExecutionRequest struct {
	// ToolCallID 是模型生成的稳定调用标识。
	ToolCallID string
	// ToolName 是已授权工具的稳定名称。
	ToolName string
	// Arguments 是通过 JSON 和 schema 校验后的原始 JSON object。
	Arguments string
}

// ToolExecutionResult 将可进入模型的安全正文与仅用于内部 digest 的细节分离。
type ToolExecutionResult struct {
	// ModelContent 是可安全写入模型 transcript 的短结果。
	ModelContent string
	// InternalDetails 可包含适配器诊断；Runtime 仅计算 digest，不写入通用事件或 transcript。
	InternalDetails string
}

// ToolExecutor 使用调用方闭包或适配器中持有的 opaque capability 执行工具。
type ToolExecutor interface {
	// Execute 必须尊重 context，且不得把 capability、凭据或内部连接信息放入 ModelContent。
	Execute(context.Context, ToolExecutionRequest) (ToolExecutionResult, error)
}

// ToolExecutorFunc 把函数适配为 ToolExecutor，便于调用方注入 request-scoped capability。
type ToolExecutorFunc func(context.Context, ToolExecutionRequest) (ToolExecutionResult, error)

// Execute 调用底层函数。
func (f ToolExecutorFunc) Execute(ctx context.Context, request ToolExecutionRequest) (ToolExecutionResult, error) {
	return f(ctx, request)
}

// AllowedTool 是一次 Run 可见且可执行的稳定工具声明。
type AllowedTool struct {
	// Name 是模型调用和审计使用的稳定工具名。
	Name string
	// Version 是调用方控制的稳定实现或 schema 版本。
	Version string
	// Description 告诉模型何时使用该工具。
	Description string
	// Parameters 是工具参数 JSON Schema object。
	Parameters map[string]any
	// Policy 声明审批、超时、副作用和并发约束。
	Policy ToolPolicy
	// Executor 持有 request-scoped opaque capability；它本身不会进入 schema 或事件。
	Executor ToolExecutor
}

// ApprovalRequest 是 ApprovalGate 评估一次调用所需的非敏感元数据。
type ApprovalRequest struct {
	// RunID 标识当前运行。
	RunID string
	// ToolCallID 标识模型调用。
	ToolCallID string
	// ToolName 是稳定工具名。
	ToolName string
	// ToolVersion 是稳定工具版本。
	ToolVersion string
	// ArgumentsDigest 是参数正文的 SHA-256 digest。
	ArgumentsDigest string
}

// ApprovalDecision 是 request-scoped 审批结果；Reason 必须是安全短码而非敏感正文。
type ApprovalDecision struct {
	// Approved 表示是否允许本次 executor 调用。
	Approved bool
	// Reason 是稳定、安全的审批原因短码。
	Reason string
}

// ApprovalGate 是具有副作用或显式要求审批的工具调用 seam。
type ApprovalGate interface {
	// Decide 必须尊重 Run context；Runtime 不保存跨 Run pending 状态。
	Decide(context.Context, ApprovalRequest) (ApprovalDecision, error)
}

// ApprovalGateFunc 把函数适配为 ApprovalGate。
type ApprovalGateFunc func(context.Context, ApprovalRequest) (ApprovalDecision, error)

// Decide 调用底层审批函数。
func (f ApprovalGateFunc) Decide(ctx context.Context, request ApprovalRequest) (ApprovalDecision, error) {
	return f(ctx, request)
}

// RunRequestOptions 保存一次 Runtime Run 的工具授权、审批能力和记忆输入。
type RunRequestOptions struct {
	// AllowedTools 是本次 Run 唯一可见且可执行的工具集合。
	AllowedTools []AllowedTool
	// KnownToolNames 仅用于区分“已知但未授权”和未知工具，不会向模型暴露 schema。
	KnownToolNames []string
	// ApprovalGate 只在 AllowedTool policy 要求审批时使用。
	ApprovalGate ApprovalGate
	// MemoryItems 是调用方已授权的、不可信的上下文数据。
	MemoryItems []MemoryItem
	// ContextSummary 是 Session 已接受的、不可信历史摘要。
	ContextSummary *ContextSummary
	// MemoryExtractor 默认关闭；启用后仅提出候选，不执行外部写入。
	MemoryExtractor MemoryExtractor
}

type governedToolSet struct {
	allowed  map[string]AllowedTool
	known    map[string]struct{}
	approval ApprovalGate
}

func newGovernedToolSet(allowed []AllowedTool, knownNames []string, approval ApprovalGate) (governedToolSet, error) {
	set := governedToolSet{
		allowed:  make(map[string]AllowedTool, len(allowed)),
		known:    make(map[string]struct{}, len(knownNames)+len(allowed)),
		approval: approval,
	}
	for _, name := range knownNames {
		name = strings.TrimSpace(name)
		if name != "" {
			set.known[name] = struct{}{}
		}
	}
	for _, tool := range allowed {
		cloned, err := cloneAllowedTool(tool)
		if err != nil {
			return governedToolSet{}, err
		}
		if err := validateAllowedTool(cloned); err != nil {
			return governedToolSet{}, err
		}
		if _, exists := set.allowed[cloned.Name]; exists {
			return governedToolSet{}, fmt.Errorf("duplicate allowed tool %q", cloned.Name)
		}
		set.allowed[cloned.Name] = cloned
		set.known[cloned.Name] = struct{}{}
	}
	return set, nil
}

func legacyAllowedTools(registry *tools.Registry) ([]AllowedTool, error) {
	if registry == nil {
		registry = tools.NewRegistry()
	}
	schemas := registry.Schemas()
	allowed := make([]AllowedTool, 0, len(schemas))
	for _, schema := range schemas {
		name := schema.Function.Name
		allowed = append(allowed, AllowedTool{
			Name:        name,
			Version:     "legacy-v1",
			Description: schema.Function.Description,
			Parameters:  schema.Function.Parameters,
			Policy:      ToolPolicy{Concurrency: ToolConcurrencyExclusive},
			Executor: ToolExecutorFunc(func(ctx context.Context, request ToolExecutionRequest) (ToolExecutionResult, error) {
				content, err := registry.Call(ctx, request.ToolName, request.Arguments)
				return ToolExecutionResult{ModelContent: content}, err
			}),
		})
	}
	return allowed, nil
}

func legacyGovernedToolSet(registry *tools.Registry) (governedToolSet, error) {
	allowed, err := legacyAllowedTools(registry)
	if err != nil {
		return governedToolSet{}, err
	}
	return newGovernedToolSet(allowed, nil, nil)
}

func (s governedToolSet) schemas() []llms.Tool {
	names := make([]string, 0, len(s.allowed))
	for name := range s.allowed {
		names = append(names, name)
	}
	slices.Sort(names)
	schemas := make([]llms.Tool, 0, len(names))
	for _, name := range names {
		tool := s.allowed[name]
		parameters, _ := cloneJSONMap(tool.Parameters)
		schemas = append(schemas, llms.Tool{
			Type: "function",
			Function: llms.ToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  parameters,
			},
		})
	}
	return schemas
}

func cloneAllowedTool(tool AllowedTool) (AllowedTool, error) {
	parameters, err := cloneJSONMap(tool.Parameters)
	if err != nil {
		return AllowedTool{}, fmt.Errorf("clone allowed tool %q schema: %w", tool.Name, err)
	}
	tool.Name = strings.TrimSpace(tool.Name)
	tool.Version = strings.TrimSpace(tool.Version)
	tool.Description = strings.TrimSpace(tool.Description)
	tool.Parameters = parameters
	if tool.Policy.Concurrency == "" {
		tool.Policy.Concurrency = ToolConcurrencyExclusive
	}
	return tool, nil
}

func validateAllowedTool(tool AllowedTool) error {
	if tool.Name == "" {
		return errors.New("allowed tool name must not be empty")
	}
	if tool.Version == "" {
		return fmt.Errorf("allowed tool %q version must not be empty", tool.Name)
	}
	if tool.Executor == nil {
		return fmt.Errorf("allowed tool %q executor must not be nil", tool.Name)
	}
	if tool.Policy.Timeout < 0 {
		return fmt.Errorf("allowed tool %q timeout must not be negative", tool.Name)
	}
	switch tool.Policy.Concurrency {
	case ToolConcurrencyExclusive:
	case ToolConcurrencyShared:
		if !tool.Policy.ReadOnly {
			return fmt.Errorf("allowed tool %q shared concurrency requires read-only policy", tool.Name)
		}
	default:
		return fmt.Errorf("allowed tool %q has unsupported concurrency policy %q", tool.Name, tool.Policy.Concurrency)
	}
	if tool.Parameters == nil {
		return fmt.Errorf("allowed tool %q parameters must not be nil", tool.Name)
	}
	return nil
}

type toolExecutionOutcome struct {
	message         ToolResultMessage
	status          ToolResultStatus
	toolVersion     string
	argumentsDigest string
	outputDigest    string
	internalDigest  string
	err             error
	startedAt       time.Time
	endedAt         time.Time
}

func (s governedToolSet) execute(ctx context.Context, runID string, call llms.ToolCall, emit func(Event) bool) toolExecutionOutcome {
	startedAt := time.Now().UTC()
	argumentsDigest := digestString(call.Function.Arguments)
	base := toolExecutionOutcome{
		status:          ToolResultSkipped,
		argumentsDigest: argumentsDigest,
		startedAt:       startedAt,
	}
	finish := func(status ToolResultStatus, version string, content string, internal string, err error) toolExecutionOutcome {
		base.status = status
		base.toolVersion = version
		base.outputDigest = digestString(content)
		base.internalDigest = digestString(internal)
		base.err = err
		base.endedAt = time.Now().UTC()
		base.message = ToolResultMessage{
			ToolCallID: call.ID,
			ToolName:   call.Function.Name,
			Content:    content,
			Status:     status,
			IsError:    status != ToolResultSuccess,
		}
		return base
	}

	tool, allowed := s.allowed[call.Function.Name]
	if !allowed {
		if _, known := s.known[call.Function.Name]; known {
			return finish(ToolResultDenied, "", safeToolDeniedContent(call.Function.Name), "tool not authorized for request", errors.New("tool not authorized"))
		}
		return finish(ToolResultSkipped, "", safeUnknownToolContent(call.Function.Name), "unknown tool", errors.New("unknown tool"))
	}
	base.toolVersion = tool.Version
	if err := validateToolArguments(tool.Parameters, call.Function.Arguments); err != nil {
		return finish(ToolResultError, tool.Version, safeToolInvalidArgumentsContent(call.Function.Name), err.Error(), errors.New("invalid tool arguments"))
	}

	if tool.Policy.RequiresApproval {
		requested := ToolApprovalRequestedEvent{
			RunID:           runID,
			ToolCallID:      call.ID,
			ToolName:        tool.Name,
			ToolVersion:     tool.Version,
			ArgumentsDigest: argumentsDigest,
			RequestedAt:     time.Now().UTC(),
		}
		if !emit(requested) {
			return finish(ToolResultCanceled, tool.Version, safeToolCanceledContent(tool.Name), "approval request event canceled", context.Canceled)
		}
		if s.approval == nil {
			emit(ToolApprovalDecidedEvent{RunID: runID, ToolCallID: call.ID, ToolName: tool.Name, ToolVersion: tool.Version, Approved: false, Decision: "missing_gate", DecidedAt: time.Now().UTC()})
			return finish(ToolResultDenied, tool.Version, safeToolDeniedContent(tool.Name), "approval gate missing", errors.New("approval gate missing"))
		}
		decision, err := s.approval.Decide(ctx, ApprovalRequest{
			RunID:           runID,
			ToolCallID:      call.ID,
			ToolName:        tool.Name,
			ToolVersion:     tool.Version,
			ArgumentsDigest: argumentsDigest,
		})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				emit(ToolApprovalDecidedEvent{RunID: runID, ToolCallID: call.ID, ToolName: tool.Name, ToolVersion: tool.Version, Approved: false, Decision: "canceled", DecidedAt: time.Now().UTC()})
				return finish(ToolResultCanceled, tool.Version, safeToolCanceledContent(tool.Name), err.Error(), ctxErr)
			}
			emit(ToolApprovalDecidedEvent{RunID: runID, ToolCallID: call.ID, ToolName: tool.Name, ToolVersion: tool.Version, Approved: false, Decision: "gate_error", DecidedAt: time.Now().UTC()})
			return finish(ToolResultDenied, tool.Version, safeToolDeniedContent(tool.Name), err.Error(), errors.New("approval failed"))
		}
		decisionCode := safeDecisionCode(decision)
		emit(ToolApprovalDecidedEvent{RunID: runID, ToolCallID: call.ID, ToolName: tool.Name, ToolVersion: tool.Version, Approved: decision.Approved, Decision: decisionCode, DecidedAt: time.Now().UTC()})
		if !decision.Approved {
			return finish(ToolResultDenied, tool.Version, safeToolDeniedContent(tool.Name), "approval denied: "+decisionCode, errors.New("approval denied"))
		}
	}

	executionCtx := ctx
	cancel := func() {}
	if tool.Policy.Timeout > 0 {
		executionCtx, cancel = context.WithTimeout(ctx, tool.Policy.Timeout)
	}
	defer cancel()
	result, err := tool.Executor.Execute(executionCtx, ToolExecutionRequest{
		ToolCallID: call.ID,
		ToolName:   tool.Name,
		Arguments:  call.Function.Arguments,
	})
	if err != nil {
		internal := result.InternalDetails
		if internal == "" {
			internal = err.Error()
		}
		if errors.Is(executionCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return finish(ToolResultTimeout, tool.Version, safeToolTimeoutContent(tool.Name), internal, context.DeadlineExceeded)
		}
		if executionCtx.Err() != nil || isCancellationError(err) {
			return finish(ToolResultCanceled, tool.Version, safeToolCanceledContent(tool.Name), internal, cancellationError(err, executionCtx))
		}
		content := strings.TrimSpace(result.ModelContent)
		if content == "" {
			content = safeToolErrorContent(tool.Name)
		}
		return finish(ToolResultError, tool.Version, content, internal, errors.New("tool execution failed"))
	}
	if err := executionCtx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return finish(ToolResultTimeout, tool.Version, safeToolTimeoutContent(tool.Name), result.InternalDetails, context.DeadlineExceeded)
		}
		return finish(ToolResultCanceled, tool.Version, safeToolCanceledContent(tool.Name), result.InternalDetails, err)
	}
	return finish(ToolResultSuccess, tool.Version, result.ModelContent, result.InternalDetails, nil)
}

func validateToolArguments(schema map[string]any, arguments string) error {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return errors.New("arguments must be a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("arguments must be valid JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("arguments must contain one JSON value")
		}
		return fmt.Errorf("arguments contain trailing data: %w", err)
	}
	return validateJSONSchemaValue(schema, value, "arguments")
}

func validateJSONSchemaValue(schema map[string]any, value any, path string) error {
	if expected, _ := schema["type"].(string); expected != "" && !matchesJSONType(expected, value) {
		return fmt.Errorf("%s must be %s", path, expected)
	}
	if enum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range enum {
			if fmt.Sprint(candidate) == fmt.Sprint(value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is not an allowed value", path)
		}
	}
	object, isObject := value.(map[string]any)
	if !isObject {
		return nil
	}
	if required, ok := schema["required"].([]any); ok {
		for _, item := range required {
			name, ok := item.(string)
			if ok {
				if _, exists := object[name]; !exists {
					return fmt.Errorf("%s.%s is required", path, name)
				}
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for name, propertyValue := range object {
		propertySchema, exists := properties[name]
		if !exists {
			if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
				return fmt.Errorf("%s.%s is not allowed", path, name)
			}
			continue
		}
		typedSchema, ok := propertySchema.(map[string]any)
		if ok {
			if err := validateJSONSchemaValue(typedSchema, propertyValue, path+"."+name); err != nil {
				return err
			}
		}
	}
	return nil
}

func matchesJSONType(expected string, value any) bool {
	switch expected {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := number.Int64()
		return err == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func skippedToolOutcome(call llms.ToolCall, version string, internal string) toolExecutionOutcome {
	now := time.Now().UTC()
	content := fmt.Sprintf("tool %q was skipped", call.Function.Name)
	return toolExecutionOutcome{
		message: ToolResultMessage{
			ToolCallID: call.ID,
			ToolName:   call.Function.Name,
			Content:    content,
			Status:     ToolResultSkipped,
			IsError:    true,
		},
		status:          ToolResultSkipped,
		toolVersion:     version,
		argumentsDigest: digestString(call.Function.Arguments),
		outputDigest:    digestString(content),
		internalDigest:  digestString(internal),
		err:             errors.New("tool call skipped"),
		startedAt:       now,
		endedAt:         now,
	}
}

func safeDecisionCode(decision ApprovalDecision) string {
	fallback := "denied"
	if decision.Approved {
		fallback = "approved"
	}
	code := strings.TrimSpace(decision.Reason)
	if code == "" || len(code) > 64 {
		return fallback
	}
	for _, char := range code {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return fallback
	}
	return code
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func safeUnknownToolContent(name string) string {
	return fmt.Sprintf("tool %q is unavailable", name)
}

func safeToolDeniedContent(name string) string {
	return fmt.Sprintf("tool %q was denied", name)
}

func safeToolInvalidArgumentsContent(name string) string {
	return fmt.Sprintf("tool %q received invalid arguments", name)
}

func safeToolTimeoutContent(name string) string {
	return fmt.Sprintf("tool %q timed out", name)
}

func safeToolCanceledContent(name string) string {
	return fmt.Sprintf("tool %q was canceled", name)
}
