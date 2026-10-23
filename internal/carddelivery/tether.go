package carddelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/hollis-labs/libs/mesh/core/hitlrouting"
	"github.com/hollis-labs/libs/mesh/core/messaging"
	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"
)

// Notifier is the part of the Tether client the sink needs.
type Notifier interface {
	Notify(ctx context.Context, req tether.NotifyRequest) (tether.NotifyResult, error)
}

// TetherSink delivers an outcome as a durable Tether message to one address.
// The address is host configuration for the sink id; a card cannot name one.
type TetherSink struct {
	N        Notifier
	From, To messaging.Address
}

// Deliver sends the outcome. A message to a recipient with no live session is
// still stored, so the asker finds it when it next reads its mail.
func (s TetherSink) Deliver(ctx context.Context, m Message) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"type": "card.outcome", "card": m.CardRef, "item_id": m.ItemID, "revision": m.Revision,
		"terminal": m.Terminal, "delivery_key": m.Key, "response": json.RawMessage(m.Envelope),
	})
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPermanent, err)
	}
	wake := m.Route.Completion != hitlrouting.Notify
	res, err := s.N.Notify(ctx, tether.NotifyRequest{
		Kind: messaging.MsgKindNotice, From: s.From, To: s.To,
		ThreadID: m.CardRef, Payload: payload, ContentType: "application/json",
		Metadata: map[string]string{"card_delivery_key": m.Key, "card_id": m.ItemID, "card_revision": fmt.Sprint(m.Revision)},
		Wake:     &wake,
	})
	if err != nil {
		var api *tether.APIError
		if errors.As(err, &api) && api.StatusCode >= 400 && api.StatusCode < 500 && api.StatusCode != http.StatusRequestTimeout && api.StatusCode != http.StatusTooManyRequests {
			return "", fmt.Errorf("%w: %v", ErrPermanent, err)
		}
		return "", err
	}
	return res.Message.ID, nil
}
