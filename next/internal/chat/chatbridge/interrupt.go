package chatbridge

import (
	"context"
	"errors"

	"github.com/google/uuid"
	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"
)

// ErrNothingToInterrupt means the session has sent no routed message to answer, so Tether has no turn to cancel.
var ErrNothingToInterrupt = errors.New("this session has no routed turn to interrupt")

// Interrupt cancels the running turn of a Tether session by replying to its most recent routed message with
// interrupt:true (Tether's only turn-level cancel). The reply body becomes the session's next turn.
func (o *Owned) Interrupt(ctx context.Context, sessionID, body string) (string, error) {
	if o.Source == nil {
		return "", errors.New("configuration: Tether connection unavailable")
	}
	in, err := o.Inbox(ctx)
	if err != nil {
		return "", err
	}
	var target *Turn
	for _, list := range [][]Turn{in.Pending, in.History} {
		for i := range list {
			t := &list[i]
			if t.SessionID != sessionID || t.Source == nil || t.Source.Origin != "routed" || t.Source.MessageID == "" || t.Source.Sender != "msg://session/local/"+sessionID {
				continue
			}
			if target == nil || t.Created > target.Created {
				target = t
			}
		}
	}
	if target == nil {
		return "", ErrNothingToInterrupt
	}
	caps, err := o.Source.RoutingCapabilities(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if caps.SessionID != sessionID || !caps.ReplyToSender {
		return "", ErrNotReplyable
	}
	r, err := o.Source.Reply(ctx, target.Source.MessageID, body, tether.ReplyOptions{Interrupt: true, IdempotencyKey: "tachyon-stop-" + uuid.NewString()})
	if err != nil {
		return "", err
	}
	return r.Interrupt, nil
}
