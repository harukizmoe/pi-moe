package agent

import (
	"context"
	"errors"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

// RunRequest 是单次 Runtime 执行使用的不可变输入快照。
// 消息保存在私有字段中，构造、读取和执行边界都会复制，避免调用方并发修改
// 已经开始执行的请求，或让 Provider 观察到创建快照之后的意外变更。
type RunRequest struct {
	messages []Message
	options  RunRequestOptions
}

// NewRunRequest 从调用方消息创建不可变快照；后续修改原切片不会影响请求。
func NewRunRequest(messages []Message) RunRequest {
	return RunRequest{messages: cloneMessages(messages), options: RunRequestOptions{}}
}

// NewRunRequestWithOptions 创建隔离输入和 request-scoped 工具能力的运行请求。
func NewRunRequestWithOptions(messages []Message, options RunRequestOptions) RunRequest {
	return RunRequest{messages: cloneMessages(messages), options: cloneRunRequestOptions(options)}
}

// Messages 返回请求消息的防御性副本，调用方修改返回值不会改变内部快照。
func (r RunRequest) Messages() []Message {
	return cloneMessages(r.messages)
}

// RunCompletedEvent 是一次 Run 成功完成时恰好出现一次的终态事件。
type RunCompletedEvent struct {
	// RunID 关联同一次 Run 的所有生命周期事件。
	RunID string
}

// AgentEvent 将 RunCompletedEvent 标记为 Agent 事件。
func (RunCompletedEvent) AgentEvent() {}

// RunFailedEvent 是非取消错误结束一次 Run 时恰好出现一次的终态事件。
type RunFailedEvent struct {
	// RunID 关联同一次 Run 的所有生命周期事件；输入校验失败时可能为空。
	RunID string
	// Error 保存导致 Run 失败的原始错误，调用方可用 errors.Is/As 分类。
	Error error
}

// AgentEvent 将 RunFailedEvent 标记为 Agent 事件。
func (RunFailedEvent) AgentEvent() {}

// RunCanceledEvent 是 context 取消或 deadline 到期时恰好出现一次的终态事件。
type RunCanceledEvent struct {
	// RunID 关联同一次 Run 的所有生命周期事件；运行开始前取消时可能为空。
	RunID string
	// Error 保存 context.Canceled 或 context.DeadlineExceeded 及其包装错误。
	Error error
}

// AgentEvent 将 RunCanceledEvent 标记为 Agent 事件。
func (RunCanceledEvent) AgentEvent() {}

// Runtime 执行 request-scoped Agent Run，并保存由装配层注入的默认 capability 快照。
type Runtime struct {
	agent          *Agent
	defaultOptions RunRequestOptions
}

// NewRuntime 使用固定 Provider、模型和运行策略创建 Runtime；单次输入仍由 RunRequest 提供，
// Runtime 不读取 Session、数据库或全局可变状态。
func NewRuntime(provider llms.Provider, registry *tools.Registry, model string, opts Options) *Runtime {
	allowed, _ := legacyAllowedTools(registry)
	return &Runtime{
		agent:          NewWithOptions(provider, registry, model, opts),
		defaultOptions: cloneRunRequestOptions(RunRequestOptions{AllowedTools: allowed}),
	}
}

// NewRuntimeWithOptions 创建带 request-scoped 默认能力的 Runtime。
func NewRuntimeWithOptions(provider llms.Provider, model string, opts Options, defaults RunRequestOptions) *Runtime {
	return &Runtime{
		agent:          NewWithOptions(provider, nil, model, opts),
		defaultOptions: cloneRunRequestOptions(defaults),
	}
}

// Run 执行一个不可变请求快照，并向返回流写入恰好一个终态事件。
// Agent 原有生命周期和消息事件保持不变；旧 RunEndEvent/ErrorEvent 仅在这里被转换，
// 因而新调用方不需要同时理解两套终态语义，现有 Agent.Stream 调用方也不会被破坏。
func (r *Runtime) Run(ctx context.Context, request RunRequest) <-chan Event {
	if ctx == nil {
		ctx = context.Background()
	}
	// 在启动 goroutine 前再次复制，并逐字段补齐默认 capability；显式 request 能力不能被其它默认值覆盖。
	request = cloneRunRequest(request)
	request.options = mergeRunRequestOptions(r.defaultOptions, request.options)
	stream := make(chan Event, 64)
	go func() {
		defer close(stream)
		// Runtime 只适配 Agent 的终态；其余过程事件保持原始顺序转发。
		underlying := r.agent.streamWithGovernedTools(ctx, request.messages, request.options)
		terminal := false
		extractionMessages := request.Messages()
		for event := range underlying {
			switch observed := event.(type) {
			case MessageEndEvent:
				extractionMessages = append(extractionMessages, cloneMessages([]Message{observed.Message})[0])
			case ToolExecutionEndEvent:
				extractionMessages = append(extractionMessages, observed.Result)
			}
			switch event := event.(type) {
			case RunEndEvent:
				if !terminal {
					if request.options.MemoryExtractor != nil {
						candidates, extractErr := request.options.MemoryExtractor.Extract(ctx, MemoryExtractionInput{
							Messages:    cloneMessages(extractionMessages),
							MemoryItems: append([]MemoryItem(nil), request.options.MemoryItems...),
						})
						if extractErr != nil {
							if !emitRuntimeEvent(ctx, stream, MemoryExtractionFailedEvent{RunID: event.RunID, Error: extractErr}) {
								return
							}
						} else if validateErr := validateMemoryCandidates(candidates); validateErr != nil {
							if !emitRuntimeEvent(ctx, stream, MemoryExtractionFailedEvent{RunID: event.RunID, Error: validateErr}) {
								return
							}
						} else if len(candidates) > 0 {
							if !emitRuntimeEvent(ctx, stream, MemoryCandidateEvent{RunID: event.RunID, Candidates: cloneMemoryCandidates(candidates)}) {
								return
							}
						}
					}
					if !emitRuntimeEvent(ctx, stream, RunCompletedEvent{RunID: event.RunID}) {
						return
					}
					terminal = true
				}
			case ErrorEvent:
				if terminal {
					continue
				}
				var terminalEvent Event
				if isCancellationError(event.Error) || ctx.Err() != nil {
					terminalEvent = RunCanceledEvent{RunID: event.RunID, Error: cancellationError(event.Error, ctx)}
				} else {
					terminalEvent = RunFailedEvent{RunID: event.RunID, Error: event.Error}
				}
				if !emitRuntimeEvent(ctx, stream, terminalEvent) {
					return
				}
				terminal = true
			default:
				if !emitRuntimeEvent(ctx, stream, event) {
					return
				}
			}
		}
		// 正常路径必须已经看到旧终态。若底层流异常关闭，则补发一个明确终态，
		// 避免调用方把“channel 已关闭”误判为成功。
		if terminal {
			return
		}
		if err := ctx.Err(); err != nil {
			emitRuntimeEvent(context.Background(), stream, RunCanceledEvent{Error: err})
			return
		}
		emitRuntimeEvent(context.Background(), stream, RunFailedEvent{Error: errors.New("runtime ended without terminal event")})
	}()
	return stream
}

func emitRuntimeEvent(ctx context.Context, stream chan<- Event, event Event) bool {
	// 先尝试无阻塞写入：即使 context 已取消，只要缓冲区仍有空间，
	// canceled 终态也必须可被正常消费流的调用方观察到。
	select {
	case stream <- event:
		return true
	default:
	}
	// 缓冲区已满时再等待消费者或取消信号，确保停止消费的调用方
	// 不会让 Runtime 转发 goroutine 永久阻塞。
	select {
	case stream <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func cloneRunRequest(request RunRequest) RunRequest {
	return RunRequest{messages: cloneMessages(request.messages), options: cloneRunRequestOptions(request.options)}
}

func cloneRunRequestOptions(options RunRequestOptions) RunRequestOptions {
	cloned := RunRequestOptions{
		KnownToolNames:  append([]string(nil), options.KnownToolNames...),
		ApprovalGate:    options.ApprovalGate,
		MemoryItems:     append([]MemoryItem(nil), options.MemoryItems...),
		ContextSummary:  cloneContextSummary(options.ContextSummary),
		MemoryExtractor: options.MemoryExtractor,
	}
	if options.AllowedTools != nil {
		cloned.AllowedTools = make([]AllowedTool, 0, len(options.AllowedTools))
		for _, tool := range options.AllowedTools {
			copy, err := cloneAllowedTool(tool)
			if err != nil {
				// 保留无效输入，由 Run stream 输出确定性的 request validation 终态。
				copy = tool
			}
			cloned.AllowedTools = append(cloned.AllowedTools, copy)
		}
	}
	return cloned
}

func isCancellationError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func cancellationError(err error, ctx context.Context) error {
	if isCancellationError(err) {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return context.Canceled
}

func cloneMemoryCandidates(candidates []MemoryCandidate) []MemoryCandidate {
	return append([]MemoryCandidate(nil), candidates...)
}

func mergeRunRequestOptions(defaults, request RunRequestOptions) RunRequestOptions {
	merged := cloneRunRequestOptions(request)
	if merged.AllowedTools == nil {
		merged.AllowedTools = cloneRunRequestOptions(defaults).AllowedTools
	}
	if merged.KnownToolNames == nil {
		merged.KnownToolNames = append([]string(nil), defaults.KnownToolNames...)
	}
	if merged.ApprovalGate == nil {
		merged.ApprovalGate = defaults.ApprovalGate
	}
	if merged.MemoryItems == nil {
		merged.MemoryItems = append([]MemoryItem(nil), defaults.MemoryItems...)
	}
	if merged.ContextSummary == nil {
		merged.ContextSummary = cloneContextSummary(defaults.ContextSummary)
	}
	if merged.MemoryExtractor == nil {
		merged.MemoryExtractor = defaults.MemoryExtractor
	}
	return merged
}
