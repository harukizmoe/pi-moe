package agent

import (
	"context"
	"errors"

	"harukizmoe/pimoe/internal/llms"
	"harukizmoe/pimoe/internal/tools"
)

// RunRequest is the immutable input snapshot for one Runtime run.
// Runtime copies Messages before execution; later caller mutations cannot
// change the request being processed.
type RunRequest struct {
	messages []Message
}

// NewRunRequest creates an immutable request snapshot from caller messages.
func NewRunRequest(messages []Message) RunRequest {
	return RunRequest{messages: cloneMessages(messages)}
}

// Messages returns a defensive copy of the request input.
func (r RunRequest) Messages() []Message {
	return cloneMessages(r.messages)
}

// RunCompletedEvent is the exactly-once successful terminal event for a run.
type RunCompletedEvent struct {
	RunID string
}

func (RunCompletedEvent) AgentEvent() {}

// RunFailedEvent is the exactly-once terminal event for a non-cancellation error.
type RunFailedEvent struct {
	RunID string
	Error error
}

func (RunFailedEvent) AgentEvent() {}

// RunCanceledEvent is the exactly-once terminal event for context cancellation.
type RunCanceledEvent struct {
	RunID string
	Error error
}

func (RunCanceledEvent) AgentEvent() {}

// Runtime executes one request-scoped Agent run and exposes typed events.
type Runtime struct {
	agent *Agent
}

// NewRuntime creates a Runtime with fixed provider/model dependencies.
// Per-run inputs remain in RunRequest; no session or global state is read.
func NewRuntime(provider llms.Provider, registry *tools.Registry, model string, opts Options) *Runtime {
	return &Runtime{agent: NewWithOptions(provider, registry, model, opts)}
}

// Run executes one immutable request snapshot and emits exactly one terminal event.
// The underlying Agent lifecycle and message events are preserved, while its
// legacy RunEndEvent/ErrorEvent terminals are translated to Runtime terminals.
func (r *Runtime) Run(ctx context.Context, request RunRequest) <-chan Event {
	if ctx == nil {
		ctx = context.Background()
	}
	request = cloneRunRequest(request)
	stream := make(chan Event, 64)
	go func() {
		defer close(stream)
		underlying := r.agent.Stream(ctx, request.messages)
		terminal := false
		for event := range underlying {
			switch event := event.(type) {
			case RunEndEvent:
				if !terminal {
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
	select {
	case stream <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func cloneRunRequest(request RunRequest) RunRequest {
	return RunRequest{messages: cloneMessages(request.messages)}
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
