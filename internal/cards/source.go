package cards

import (
	"context"
	"errors"
	"fmt"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

// SourceRef names the record in another application that a mirrored card
// stands for. The broker keeps only this reference and a mirror of the state;
// the application remains the owner.
type SourceRef struct {
	App      string `json:"app"`
	ID       string `json:"id"`
	Revision string `json:"revision,omitempty"`
}

// SourceState is the authoritative state read back from the owning
// application.
type SourceState struct {
	Ref SourceRef
	// Terminal reports whether the source considers the record settled.
	Terminal bool
	// Resolved reports that the settlement was an answer, not a cancel/expiry.
	Resolved bool
	Response *hitl.Response
}

// Source is the contract an application implements to have its own requests
// (for example a Torque checkpoint or a Tangent interaction) appear as cards.
// Every method acts on the application's own store; Tachyon writes nothing
// else, so a card has exactly one owner.
type Source interface {
	// App is the application id the verified credential carries.
	App() string
	// Mirror reads the current state of one source record.
	Mirror(ctx context.Context, ref SourceRef) (SourceState, error)
	// Respond writes the operator's answer into the owning application.
	Respond(ctx context.Context, ref SourceRef, resp hitl.Response, who hitl.Participant) error
}

// ErrSourceMismatch means the source did not confirm the answer that was just
// written, so the card must not be shown as answered.
var ErrSourceMismatch = errors.New("source did not confirm the response")

// RespondThrough answers a mirrored card by writing to its source and then
// verifying by an authoritative read there. It returns the source's state
// only when the source itself reports the record resolved. There is no second
// write: the broker's mirror is updated from what the read returns.
func RespondThrough(ctx context.Context, src Source, ref SourceRef, resp hitl.Response, who hitl.Participant) (SourceState, error) {
	if ref.App != src.App() {
		return SourceState{}, fmt.Errorf("%w: reference belongs to %q, not %q", hitl.ErrInvalidRequest, ref.App, src.App())
	}
	if err := src.Respond(ctx, ref, resp, who); err != nil {
		return SourceState{}, err
	}
	state, err := src.Mirror(ctx, ref)
	if err != nil {
		return SourceState{}, err
	}
	if !state.Terminal || !state.Resolved {
		return state, ErrSourceMismatch
	}
	return state, nil
}
