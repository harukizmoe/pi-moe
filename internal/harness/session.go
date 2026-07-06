package harness

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"harukizmoe/pimoe/internal/agent"
	"harukizmoe/pimoe/internal/llms"
)

// Session 保存一次多 turn Agent 对话的内存态 transcript 和运行控制。
type Session struct {
	mu        sync.Mutex
	agent     *agent.Agent
	messages  []agent.Message
	cancel    context.CancelFunc
	listeners map[chan Event]struct{}
}

// NewSession 创建一个持有独立 transcript 的 Agent 会话。
func (h *Harness) NewSession() *Session {
	return &Session{
		agent:     h.agent,
		listeners: make(map[chan Event]struct{}),
	}
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

	ctx, cancel := context.WithCancel(ctx)
	userMessage := agent.UserMessage{Content: trimmed}

	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		cancel()
		return closedErrorStream(fmt.Errorf("active turn already running"))
	}
	s.cancel = cancel
	s.messages = append(s.messages, userMessage)
	snapshot := cloneSessionMessages(s.messages)
	s.mu.Unlock()

	out := make(chan Event)
	go s.runPrompt(ctx, cancel, snapshot, out)
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

func (s *Session) runPrompt(ctx context.Context, cancel context.CancelFunc, snapshot []agent.Message, out chan<- Event) {
	defer close(out)
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	for event := range s.agent.Stream(ctx, snapshot) {
		s.applyTerminalEvent(event)
		s.publish(event)
		if !emitHarnessEvent(ctx, out, event) {
			return
		}
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
