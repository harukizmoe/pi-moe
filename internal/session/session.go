package session

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/llms"
)

// Config 保存创建 Session 所需的 Agent 装配配置。
type Config = agent.Config

// Session 保存一次多 turn Agent 对话的内存态 transcript 和运行控制。
type Session struct {
	// mu 保护 messages、cancel 和 listeners；Agent Stream 在锁外执行，避免阻塞订阅者和取消路径。
	mu sync.Mutex
	// agent 是已装配好的执行器，Session 只负责喂 transcript 和消费 terminal events。
	agent *agent.Agent
	// messages 只保存 terminal transcript：user、assistant 完整消息和 tool result；delta 事件不进入历史。
	messages []agent.Message
	// cancel 指向当前运行中的 prompt；非 nil 表示禁止并发 turn。
	cancel context.CancelFunc
	// listeners 保存事件订阅者，写满的订阅者会被移除，避免慢消费者拖住主流程。
	listeners map[chan Event]struct{}
	// store 非 nil 时启用 JSONL 持久化；New 创建的纯内存 Session 保持原行为。
	store *fileStore
}

// New 创建一个持有独立 transcript 的 Agent 会话。
func New(ctx context.Context, cfg Config) (*Session, error) {
	runner, err := agent.NewConfigured(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Session{
		agent:     runner,
		listeners: make(map[chan Event]struct{}),
	}, nil
}

// Open 创建或恢复一个文件持久化 Session；只有完整 run 推进恢复点，取消中的 prompt 不会落盘。
func Open(ctx context.Context, cfg Config, path string) (*Session, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("session path must not be empty")
	}
	runner, err := agent.NewConfigured(ctx, cfg)
	if err != nil {
		return nil, err
	}
	store := newFileStore(path)
	messages, err := store.load()
	if err != nil {
		return nil, err
	}
	return &Session{
		agent:     runner,
		messages:  messages,
		listeners: make(map[chan Event]struct{}),
		store:     store,
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
	snapshot := cloneSessionMessages(s.messages)
	s.mu.Unlock()

	out := make(chan Event, 64)
	go s.runPrompt(ctx, cancel, snapshot, baseLen, out)
	return out
}

// Events 订阅 session 后续事件；订阅者必须持续读取以避免被移除。
func (s *Session) Events() <-chan Event {
	ch := make(chan Event, 64)
	s.mu.Lock()
	s.listeners[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

// Messages 返回当前 transcript 的防御性快照。
func (s *Session) Messages() []agent.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSessionMessages(s.messages)
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

// runPrompt 桥接 Agent 事件流和 Session 状态：先更新内存，再广播事件，RunEnd 后提交持久化。
func (s *Session) runPrompt(ctx context.Context, cancel context.CancelFunc, snapshot []agent.Message, baseLen int, out chan<- Event) {
	defer close(out)
	completed := false
	canceled := false
	defer func() {
		cancel()
		if canceled && !completed {
			s.discardMessagesFrom(baseLen)
		}
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	for event := range s.agent.Stream(ctx, snapshot) {
		// 只有 Agent 发出 RunEnd 才说明本轮 terminal transcript 完整，可安全推进 durable leaf。
		if runEnd, ok := event.(RunEndEvent); ok {
			if err := s.persistMessagesFrom(baseLen); err != nil {
				terminal := ErrorEvent{RunID: runEnd.RunID, Error: err}
				s.publish(terminal)
				emitSessionTerminal(out, terminal)
				return
			}
			completed = true
		}
		if _, ok := event.(ErrorEvent); ok && ctx.Err() != nil {
			canceled = true
			s.publish(event)
			emitSessionTerminal(out, event)
			return
		}

		// 只把 terminal 事件纳入 transcript；MessageDelta 等流式 UI 事件不参与恢复。
		s.applyTerminalEvent(event)
		s.publish(event)
		if !emitSessionEvent(ctx, out, event) {
			if err := ctx.Err(); err != nil && !completed {
				canceled = true
				terminal := ErrorEvent{Error: err}
				s.publish(terminal)
				emitSessionTerminal(out, terminal)
			}
			return
		}
	}
	if err := ctx.Err(); err != nil && !completed {
		canceled = true
		terminal := ErrorEvent{Error: err}
		s.publish(terminal)
		emitSessionTerminal(out, terminal)
		return
	}
	if !completed {
		if err := s.persistMessagesFrom(baseLen); err != nil {
			terminal := ErrorEvent{Error: err}
			s.publish(terminal)
			emitSessionTerminal(out, terminal)
		}
	}
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

// persistMessagesFrom 把本轮新增 transcript 复制到锁外写盘，避免文件 IO 阻塞 Session 状态锁。
func (s *Session) persistMessagesFrom(index int) error {
	s.mu.Lock()
	store := s.store
	if store == nil || index >= len(s.messages) {
		s.mu.Unlock()
		return nil
	}
	messages := cloneSessionMessages(s.messages[index:])
	s.mu.Unlock()

	if len(messages) == 0 {
		return nil
	}
	if err := store.appendMessages(messages); err != nil {
		return fmt.Errorf("persist session messages: %w", err)
	}
	return nil
}

func (s *Session) publish(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for listener := range s.listeners {
		select {
		case listener <- event:
		default:
			close(listener)
			delete(s.listeners, listener)
		}
	}
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
	select {
	case stream <- event:
	default:
	}
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
