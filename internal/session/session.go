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
	mu        sync.Mutex
	agent     *agent.Agent
	messages  []agent.Message
	cancel    context.CancelFunc
	listeners map[chan Event]struct{}
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
		if _, ok := event.(RunEndEvent); ok {
			completed = true
		}
		if _, ok := event.(ErrorEvent); ok && ctx.Err() != nil {
			canceled = true
			s.publish(event)
			emitSessionTerminal(out, event)
			return
		}

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
	}
}

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

func (s *Session) discardMessagesFrom(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < len(s.messages) {
		s.messages = s.messages[:index]
	}
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
