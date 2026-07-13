package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/llms"
)

// RuntimeFactory 已由应用层完成 Provider、capability 和策略装配后注入 Session。
type RuntimeFactory func(context.Context) (*agent.Runtime, error)

// Session 保存一次多 turn Agent 对话的内存态 transcript、运行控制和已提交记忆候选。
type Session struct {
	// mu 保护 messages、memoryCandidates 和 cancel；Runtime 在锁外执行，避免阻塞取消路径。
	mu sync.Mutex
	// runtime 是调用方注入的执行边界，Session 不拥有 Provider 或 Tool Registry。
	runtime *agent.Runtime
	// messages 只保存 terminal transcript：user、assistant 完整消息和 tool result；delta 事件不进入历史。
	messages []agent.Message
	// contextSummary 是 completed Run 接受的摘要；summarizedMessages 是其覆盖的 transcript 前缀长度。
	contextSummary     *agent.ContextSummary
	summarizedMessages int
	// memoryCandidates 仅保存 completed Run 的候选，失败/取消不会推进。
	memoryCandidates []agent.MemoryCandidate
	// cancel 指向当前运行中的 prompt；非 nil 表示禁止并发 turn。
	cancel context.CancelFunc
	// store 非 nil 时启用 JSONL 持久化；NewWithRuntime 创建的纯内存 Session 保持原行为。
	store *fileStore
}

// NewWithRuntime 创建一个持有独立 transcript 的 Runtime 会话。
func NewWithRuntime(runtime *agent.Runtime) (*Session, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime must not be nil")
	}
	return &Session{runtime: runtime}, nil
}

// OpenWithRuntime 创建或恢复一个文件持久化 Runtime 会话。
func OpenWithRuntime(runtime *agent.Runtime, path string) (*Session, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime must not be nil")
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("session path must not be empty")
	}
	store := newFileStore(path)
	state, err := store.load()
	if err != nil {
		return nil, err
	}
	return &Session{
		runtime:            runtime,
		messages:           state.messages,
		contextSummary:     cloneSessionContextSummary(state.contextSummary),
		summarizedMessages: state.summarizedMessages,
		store:              store,
	}, nil
}

// Prompt 追加用户输入并启动一轮 Agent 运行；返回的 channel 只包含本轮事件。
func (s *Session) Prompt(ctx context.Context, input string) <-chan Event {
	if ctx == nil {
		ctx = context.Background()
	}
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return closedErrorStream(fmt.Errorf("empty input"))
	}
	if err := ctx.Err(); err != nil {
		return closedErrorStream(err)
	}

	ctx, cancel := context.WithCancel(ctx)
	userMessage := agent.UserMessage{Content: trimmed}

	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		cancel()
		return closedErrorStream(fmt.Errorf("active turn already running"))
	}
	s.cancel = cancel
	// baseLen 是本轮开始前的 transcript 边界；取消时回滚，成功时只持久化新增部分。
	baseLen := len(s.messages)
	s.messages = append(s.messages, userMessage)
	summaryBase := s.summarizedMessages
	snapshot := cloneSessionMessages(s.messages[summaryBase:])
	contextSummary := cloneSessionContextSummary(s.contextSummary)
	s.mu.Unlock()

	out := make(chan Event, 64)
	go s.runPrompt(ctx, cancel, snapshot, baseLen, summaryBase, contextSummary, out)
	return out
}

// Messages 返回当前 transcript 的防御性快照。
func (s *Session) Messages() []agent.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSessionMessages(s.messages)
}

// MemoryCandidates 返回最近一次 completed Run 提交的候选快照。
func (s *Session) MemoryCandidates() []agent.MemoryCandidate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agent.MemoryCandidate(nil), s.memoryCandidates...)
}

// Cancel 取消当前运行；没有运行时是 no-op。
func (s *Session) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// runPrompt 桥接 Runtime 事件和 Session 状态：先更新内存，成功终态后提交，失败终态回滚。
func (s *Session) runPrompt(ctx context.Context, cancel context.CancelFunc, snapshot []agent.Message, baseLen, summaryBase int, contextSummary *agent.ContextSummary, out chan<- Event) {
	defer close(out)
	completed := false
	pendingCandidates := []agent.MemoryCandidate(nil)
	var pendingSummary *agent.ContextSummaryCandidate
	defer func() {
		cancel()
		if !completed {
			s.discardMessagesFrom(baseLen)
		}
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	request := agent.NewRunRequestWithOptions(snapshot, agent.RunRequestOptions{ContextSummary: contextSummary})
	for event := range s.runtime.Run(ctx, request) {
		switch event := event.(type) {
		case ContextSummaryCandidateEvent:
			candidate := event.Candidate
			pendingSummary = &candidate
			continue
		case MemoryCandidateEvent:
			pendingCandidates = append([]agent.MemoryCandidate(nil), event.Candidates...)
			continue
		case RunCompletedEvent:
			if pendingSummary != nil && (pendingSummary.ReplacedMessages < 0 || pendingSummary.ReplacedMessages > len(snapshot)) {
				s.discardMessagesFrom(baseLen)
				emitSessionTerminal(out, RunFailedEvent{RunID: event.RunID, Error: fmt.Errorf("invalid context summary replacement count %d", pendingSummary.ReplacedMessages)})
				return
			}
			totalSummarized := summaryBase
			if pendingSummary != nil {
				totalSummarized += pendingSummary.ReplacedMessages
			}
			if err := s.persistRun(baseLen, pendingSummary, totalSummarized); err != nil {
				s.discardMessagesFrom(baseLen)
				emitSessionTerminal(out, RunFailedEvent{RunID: event.RunID, Error: err})
				return
			}
			s.mu.Lock()
			s.memoryCandidates = append([]agent.MemoryCandidate(nil), pendingCandidates...)
			if pendingSummary != nil {
				summary := pendingSummary.Summary
				s.contextSummary = &summary
				s.summarizedMessages = totalSummarized
			}
			s.mu.Unlock()
			completed = true
			emitSessionTerminal(out, event)
			return
		case RunFailedEvent:
			emitSessionTerminal(out, event)
			return
		case RunCanceledEvent:
			emitSessionTerminal(out, event)
			return
		}

		s.applyTerminalEvent(event)
		if ctx == nil || ctx.Err() == nil {
			_ = emitSessionEvent(ctx, out, event)
		}
	}
	emitSessionTerminal(out, RunFailedEvent{Error: errors.New("runtime ended without terminal event")})
}

// applyTerminalEvent 将 Agent 终态事件转换为 Session transcript 事实。
func (s *Session) applyTerminalEvent(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch event := event.(type) {
	case MessageEndEvent:
		s.messages = append(s.messages, cloneAssistantMessage(event.Message))
	case ToolExecutionEndEvent:
		s.messages = append(s.messages, event.Result)
	}
}

// discardMessagesFrom 回滚取消中的本轮消息，保证内存态与 durable leaf 的语义一致。
func (s *Session) discardMessagesFrom(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < len(s.messages) {
		s.messages = s.messages[:index]
	}
}

// persistRun 将本轮 transcript 与已接受 summary 在一次 append-only leaf 中提交。
func (s *Session) persistRun(index int, summary *agent.ContextSummaryCandidate, summarizedMessages int) error {
	s.mu.Lock()
	store := s.store
	if store == nil || index >= len(s.messages) {
		s.mu.Unlock()
		return nil
	}
	messages := cloneSessionMessages(s.messages[index:])
	s.mu.Unlock()

	if err := store.appendRun(messages, summary, summarizedMessages); err != nil {
		return fmt.Errorf("persist session run: %w", err)
	}
	return nil
}

func cloneSessionContextSummary(summary *agent.ContextSummary) *agent.ContextSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	return &cloned
}

func emitSessionEvent(ctx context.Context, stream chan<- Event, event Event) bool {
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

func emitSessionTerminal(stream chan<- Event, event Event) {
	stream <- event
}

func closedErrorStream(err error) <-chan Event {
	stream := make(chan Event, 1)
	stream <- ErrorEvent{Error: err}
	close(stream)
	return stream
}

func cloneSessionMessages(messages []agent.Message) []agent.Message {
	out := make([]agent.Message, len(messages))
	for i, message := range messages {
		switch msg := message.(type) {
		case agent.UserMessage:
			out[i] = msg
		case agent.AssistantMessage:
			out[i] = cloneAssistantMessage(msg)
		case agent.ToolResultMessage:
			out[i] = msg
		default:
			out[i] = message
		}
	}
	return out
}

func cloneAssistantMessage(message agent.AssistantMessage) agent.AssistantMessage {
	return agent.AssistantMessage{
		Content:   message.Content,
		ToolCalls: append([]llms.ToolCall(nil), message.ToolCalls...),
	}
}
