package llms

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type collectChatProvider struct {
	stream <-chan ChatStreamEvent
	err    error
}

func (p collectChatProvider) ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatStreamEvent, error) {
	return p.stream, p.err
}

func TestCollectChatReturnsDoneMessage(t *testing.T) {
	stream := make(chan ChatStreamEvent, 1)
	stream <- ChatStreamEvent{Type: ChatStreamEventTypeDone, Message: Message{Role: RoleAssistant, Content: "done"}}
	close(stream)

	resp, err := CollectChat(context.Background(), collectChatProvider{stream: stream}, ChatRequest{})
	if err != nil {
		t.Fatalf("CollectChat() error = %v", err)
	}
	if resp == nil {
		t.Fatal("CollectChat() response = nil")
	}
	if resp.Message.Role != RoleAssistant || resp.Message.Content != "done" {
		t.Fatalf("CollectChat() message = %#v", resp.Message)
	}
}

func TestCollectChatReturnsSetupError(t *testing.T) {
	sentinel := errors.New("setup failed")

	_, err := CollectChat(context.Background(), collectChatProvider{err: sentinel}, ChatRequest{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("CollectChat() error = %v, want %v", err, sentinel)
	}
}

func TestCollectChatReturnsStreamError(t *testing.T) {
	sentinel := errors.New("stream failed")
	stream := make(chan ChatStreamEvent, 1)
	stream <- ChatStreamEvent{Type: ChatStreamEventTypeError, Err: sentinel}
	close(stream)

	_, err := CollectChat(context.Background(), collectChatProvider{stream: stream}, ChatRequest{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("CollectChat() error = %v, want %v", err, sentinel)
	}
}

func TestCollectChatReturnsErrorWhenStreamClosesWithoutDone(t *testing.T) {
	stream := make(chan ChatStreamEvent)
	close(stream)

	_, err := CollectChat(context.Background(), collectChatProvider{stream: stream}, ChatRequest{})
	if err == nil {
		t.Fatal("CollectChat() error = nil")
	}
	if !strings.Contains(err.Error(), "ended without done") {
		t.Fatalf("CollectChat() error = %v, want ended without done", err)
	}
}
