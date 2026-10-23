package cardevents

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/cardfx"
	"github.com/hollis-labs/tachyon/internal/cards"
)

// App is the producer name the hub binds the credential to.
const App = "tachyon"

// Collector turns card state in the queue into outbox events.
type Collector struct {
	Items   *approvals.Store
	Effects *cardfx.Store
	Outbox  *Store
	Now     func() time.Time
}

func subject(itemID string) string { return "msg://card/tachyon/" + itemID }

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// Pass queues the events for every item not yet fully queued. It returns after
// one sweep; the caller repeats it.
func (c *Collector) Pass(ctx context.Context) error {
	ids, err := c.Outbox.Unscanned(ctx, 200)
	if err != nil {
		return err
	}
	for _, id := range ids {
		rec, err := c.Items.Get(ctx, id)
		if err != nil {
			return err
		}
		done, err := c.collect(ctx, rec)
		if err != nil {
			return err
		}
		if done {
			if err := c.Outbox.Done(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Collector) queue(ctx context.Context, rec hitl.Record, kind string, payload map[string]any, at time.Time) error {
	id := runtimeevents.NewEventID()
	_, err := c.Outbox.Queue(ctx, rec.ItemID, kind, id, func(seq int64) ([]byte, error) {
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return json.Marshal(runtimeevents.Event{
			SchemaVersion: runtimeevents.SchemaVersion, ID: id, Kind: runtimeevents.EventKind(kind), Time: at, App: App,
			Sequence: uint64(seq), Generation: 1, Subject: subject(rec.ItemID), Source: runtimeevents.Source{Channel: "outbox"},
			Payload: body, Class: &runtimeevents.Class{Category: "card", PayloadSchema: payloadSchema(kind)},
		})
	}, c.now())
	return err
}

func payloadSchema(kind string) string {
	e, _ := catalog.Lookup(kind)
	return e.PayloadSchema
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// collect queues what is due for one item and reports whether nothing more can
// become due.
func (c *Collector) collect(ctx context.Context, rec hitl.Record) (bool, error) {
	var req hitl.EnqueueRequest
	if json.Unmarshal(rec.Request, &req) != nil || !cards.IsCard(req.Kind) {
		return true, nil
	}
	base := func(at time.Time) map[string]any {
		p := map[string]any{"card_id": rec.ItemID, "revision": rec.Revision, "card_type": strings.TrimPrefix(req.Kind, cards.KindPrefix), "source_app": rec.CallerScope, "at": at.UTC().Format(time.RFC3339)}
		if v := str(req.Extra["class"]); v != "" {
			p["risk"] = v
		}
		if v := str(req.Extra["op_digest"]); v != "" {
			p["op_digest"] = v
		}
		return p
	}
	created := base(rec.CreatedAt)
	created["revision"], created["state"] = 1, "pending"
	if rec.ExpiresAt != nil {
		created["expires_at"] = rec.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if err := c.queue(ctx, rec, catalog.KindCardCreated, created, rec.CreatedAt); err != nil {
		return false, err
	}
	if !rec.State.IsTerminal() {
		return false, nil
	}
	ended := rec.UpdatedAt
	if rec.Outcome != nil {
		ended = rec.Outcome.OutcomeTime()
	}
	p := base(ended)
	switch o := rec.Outcome.(type) {
	case hitl.Resolved:
		p["outcome"] = o.Resolution.Response.Decision
		p["assurance"] = o.Resolution.Participant.Assurance
		if err := c.queue(ctx, rec, catalog.KindCardResolved, p, ended); err != nil {
			return false, err
		}
	case hitl.Expired:
		if err := c.queue(ctx, rec, catalog.KindCardExpired, p, ended); err != nil {
			return false, err
		}
	default:
		p["state"] = "canceled"
		if rec.State == hitl.StateSuperseded {
			p["state"] = "superseded"
		}
		if err := c.queue(ctx, rec, catalog.KindCardUpdated, p, ended); err != nil {
			return false, err
		}
	}
	// An approved card with effects is not done until its receipt is final.
	res, isResolved := rec.Outcome.(hitl.Resolved)
	if !isResolved || res.Resolution.Response.Decision != "approved" || len(req.Extra["effects"]) == 0 {
		return true, nil
	}
	receipt, err := c.Effects.Get(ctx, rec.ItemID)
	if err != nil || receipt == nil || receipt.State == cardfx.StateRunning {
		return false, err
	}
	if receipt.State == cardfx.StateNone {
		return true, nil
	}
	at := ended
	if receipt.EndedAt != nil {
		at = *receipt.EndedAt
	}
	e := base(at)
	results := make([]map[string]any, 0, len(receipt.Effects))
	for _, r := range receipt.Effects {
		results = append(results, map[string]any{"index": r.Index, "status": r.Status})
	}
	e["results"] = results
	kind := catalog.KindCardEffectCompleted
	if receipt.State != cardfx.StateCompleted {
		kind = catalog.KindCardEffectFailed
		e["error_code"] = receipt.State // failed | refused
	}
	if err := c.queue(ctx, rec, kind, e, at); err != nil {
		return false, err
	}
	return true, nil
}
