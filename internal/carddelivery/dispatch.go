package carddelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/hitlrouting"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/cards"
)

// maxAttempts bounds retries of a sink that keeps refusing.
const maxAttempts = 8

// Message is what a sink is asked to deliver. Key is stable across retries of
// the same (item, revision), so a sink can drop a duplicate.
type Message struct {
	Key      string
	ItemID   string
	Revision int64
	Source   string
	Route    hitlrouting.Route
	Terminal string // resolved | canceled | expired
	Envelope json.RawMessage
	CardRef  string
}

// Sink delivers one message and returns a receipt. A returned error is retried
// with backoff; ErrPermanent ends the delivery as failed.
type Sink interface {
	Deliver(ctx context.Context, m Message) (receipt string, err error)
}

// ErrPermanent marks a refusal retrying cannot fix.
var ErrPermanent = errors.New("permanent delivery failure")

// Dispatcher routes terminal cards and delivers them. Sinks are keyed by the id
// the profile's rules name; the profile does not know what a sink is. The
// sink named Hold is the asker pulling through card.await/card.get: nothing is
// pushed for it.
type Dispatcher struct {
	Items   *approvals.Store
	Store   *Store
	Router  *hitlrouting.Router
	Sinks   map[string]Sink
	Hold    string
	Now     func() time.Time
	Backoff func(attempt int) time.Duration
}

// New returns a dispatcher with the default clock and backoff.
func New(items *approvals.Store, store *Store, router *hitlrouting.Router, hold string, sinks map[string]Sink) *Dispatcher {
	return &Dispatcher{Items: items, Store: store, Router: router, Sinks: sinks, Hold: hold,
		Now: func() time.Time { return time.Now().UTC() },
		Backoff: func(n int) time.Duration {
			d := 5 * time.Second << min(n, 8)
			return min(d, 10*time.Minute)
		}}
}

// Watch runs passes until ctx ends.
func (d *Dispatcher) Watch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 3 * time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		if err := d.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("card delivery pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Run makes one pass: claim and route newly terminal cards, then retry due ones.
func (d *Dispatcher) Run(ctx context.Context) error {
	rows, err := d.Items.DB.Reader.QueryContext(ctx, "SELECT id FROM approval_items WHERE terminal=1 AND state IN ('resolved','canceled','expired') AND id NOT IN (SELECT item_id FROM card_deliveries) ORDER BY created_ns LIMIT 50")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		rec, err := d.Items.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := d.route(ctx, rec); err != nil {
			return err
		}
	}
	due, err := d.Store.Due(ctx, d.Now(), 50)
	if err != nil {
		return err
	}
	for _, row := range due {
		rec, err := d.Items.Get(ctx, row.ItemID)
		if err != nil {
			return err
		}
		if err := d.send(ctx, rec, row); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) route(ctx context.Context, rec hitl.Record) error {
	now := d.Now()
	row := Row{ItemID: rec.ItemID, Revision: rec.Revision, Key: "tachyon-card-" + uuid.NewString(), State: StateHeld}
	var req hitl.EnqueueRequest
	if json.Unmarshal(rec.Request, &req) != nil || !strings.HasPrefix(req.Kind, "card.") {
		row.Error = "not a card"
		_, err := d.Store.Claim(ctx, row, now)
		return err
	}
	route, err := d.Router.Resolve(ctx, rec.ItemID, hitlrouting.Subject{Kind: req.Kind, Source: req.Source.ApplicationID})
	row.Sink, row.RuleID, row.Completion, row.Delivery, row.ProfileHash = route.Sink, route.RuleID, string(route.Completion), string(route.Delivery), d.Router.Hash()
	switch {
	case err != nil:
		row.State, row.Error = StateFailed, err.Error()
	case route.Delivery == hitlrouting.Await || route.Sink == d.Hold:
		row.State = StateHeld
	default:
		row.State = StatePending
	}
	won, err := d.Store.Claim(ctx, row, now)
	if err != nil || !won || row.State != StatePending {
		return err
	}
	return d.send(ctx, rec, row)
}

func (d *Dispatcher) send(ctx context.Context, rec hitl.Record, row Row) error {
	now := d.Now()
	sink, ok := d.Sinks[row.Sink]
	if !ok {
		return d.Store.Settle(ctx, row.ItemID, row.Revision, StateFailed, "", fmt.Sprintf("sink %q is not configured on this host", row.Sink), now, now)
	}
	var req hitl.EnqueueRequest
	_ = json.Unmarshal(rec.Request, &req)
	msg := Message{Key: row.Key, ItemID: rec.ItemID, Revision: rec.Revision, Source: req.Source.ApplicationID, CardRef: "tach:card/" + rec.ItemID, Terminal: string(rec.State)}
	msg.Route.Sink, msg.Route.RuleID = row.Sink, row.RuleID
	msg.Route.Completion, msg.Route.Delivery = hitlrouting.Completion(row.Completion), hitlrouting.Delivery(row.Delivery)
	var resp *hitl.Response
	at := rec.UpdatedAt
	if r, ok := rec.Outcome.(hitl.Resolved); ok {
		resp, at = &r.Resolution.Response, r.Resolution.ResolvedAt
	}
	env, err := json.Marshal(cards.ResponseToEnvelope(req.IdempotencyKey, rec.State, resp, at))
	if err != nil {
		return d.Store.Settle(ctx, row.ItemID, row.Revision, StateFailed, "", err.Error(), now, now)
	}
	msg.Envelope = env
	receipt, err := sink.Deliver(ctx, msg)
	switch {
	case err == nil:
		return d.Store.Settle(ctx, row.ItemID, row.Revision, StateSent, receipt, "", now, now)
	case errors.Is(err, ErrPermanent) || row.Attempts+1 >= maxAttempts:
		return d.Store.Settle(ctx, row.ItemID, row.Revision, StateFailed, "", err.Error(), now, now)
	default:
		return d.Store.Settle(ctx, row.ItemID, row.Revision, StatePending, "", err.Error(), now.Add(d.Backoff(row.Attempts)), now)
	}
}
