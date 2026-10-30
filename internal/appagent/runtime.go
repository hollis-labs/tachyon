package appagent

import (
	"context"
	"errors"
)

// Runtime is the port between Tachyon's chat and whatever runs the agent. Nanite
// is the temporary adapter; an embedded runtime would be a second implementation.
// Nothing outside the adapter may know a runtime's wire format.
type Runtime interface {
	// Ensure returns a usable session, reusing existing when it is still alive.
	Ensure(ctx context.Context, existing string) (session string, err error)
	// Submit starts a turn and returns the runtime's id for it. ErrSessionGone
	// means the session no longer exists: Ensure a new one and submit again.
	Submit(ctx context.Context, session, text string) (turn string, err error)
	// Observe streams one turn's events until the turn finishes, fails, or ctx ends.
	Observe(ctx context.Context, session, turn string, emit func(TurnEvent)) error
	// Result is the authoritative snapshot of one turn; it survives a lost stream.
	Result(ctx context.Context, session, turn string) (Result, error)
}

var (
	ErrSessionGone   = errors.New("runtime session no longer exists")
	ErrRuntimeDown   = errors.New("the agent runtime is unavailable")
	ErrNotConfigured = errors.New("the app agent is not configured")
)

// TurnEvent kinds.
const (
	EventText      = "text"       // Text is the next piece of the reply
	EventToolStart = "tool_start" // Tool is running
	EventToolEnd   = "tool_end"   // Tool finished (ok | error)
	EventFinish    = "finish"     // Reason says why; the turn is over
	EventError     = "error"      // Err says what failed; the turn is over
)

type TurnEvent struct {
	Kind   string
	Text   string
	Tool   Tool
	Reason string
	Err    string
}

// Result is a turn's state as the runtime sees it now.
type Result struct {
	State string // submitted | working | completed | failed | canceled | input_required
	Text  string
	Tools []Tool
	Error string
}

func (r Result) Terminal() bool {
	switch r.State {
	case "completed", "failed", "canceled", "input_required":
		return true
	}
	return false
}

type clientContextKey struct{}

// WithClientContext carries a turn's client_context payload to the runtime adapter. The
// adapter decides whether to forward it; the server sets it only when enabled.
func WithClientContext(ctx context.Context, v any) context.Context {
	return context.WithValue(ctx, clientContextKey{}, v)
}

// ClientContextFrom returns the payload set by WithClientContext, or nil.
func ClientContextFrom(ctx context.Context) any { return ctx.Value(clientContextKey{}) }
