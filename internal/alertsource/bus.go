package alertsource

import (
	"context"
	"strconv"

	"github.com/hollis-labs/tachyon/internal/agentscollector"
	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

// BusSource is the in-process Agents bus_events feed. The collector has no subscription API yet (Agents A-tasks),
// so AL2 defines the seam: the wiring slice implements it over the collector's store. Until then the
// adapter is exercised with a fake.
type BusSource interface {
	// Events returns bus events with Seq greater than after, oldest first.
	Events(ctx context.Context, after int64) ([]agentscollector.BusEvent, error)
}

// FromBus normalizes one bus event. Bus types are feed types (deploy, restart, host, alert ...), not rule-pack
// kinds, so the kind is "bus.<type>": the default pack's catch-all shows them at notice and above, and a rule
// can target them explicitly. tether_seq is the source event id (stable across restarts of this process).
func FromBus(b agentscollector.BusEvent) Event {
	e := Event{Source: "agents", SourceEventID: strconv.FormatInt(b.Seq, 10), Kind: "bus." + Clean(b.Type, 40), AtMS: b.At.UnixMilli(),
		SeverityHint: validSeverity(b.Severity), Payload: map[string]any{"actor": Clean(b.Actor, 80), "app": Clean(b.App, 40), "subject": Clean(b.Subject, 120)}}
	if b.Card != nil {
		e.Payload["title"], e.Payload["summary"] = Clean(b.Card.Title, 120), Clean(b.Card.Summary, 300)
	}
	for _, r := range b.Refs {
		if r.Kind != "" && r.Ref != "" {
			e.Subjects = append(e.Subjects, alertsapi.Subject{Kind: Clean(r.Kind, 40), Ref: Clean(r.Ref, 200), Label: Clean(r.Label, 120)})
		}
	}
	return e
}

// Bus polls a BusSource and feeds the sink, keeping the last Seq as its cursor.
type Bus struct {
	Src    BusSource
	Sink   Sink
	Cursor CursorStore
	Health *Health
	Limit  *Limiter
}

// Poll drains everything after the stored cursor once. The supervisor calls it on a ticker.
func (b *Bus) Poll(ctx context.Context) error {
	cur, err := b.Cursor.Cursor("agents")
	if err != nil {
		return err
	}
	after, _ := strconv.ParseInt(cur, 10, 64)
	evs, err := b.Src.Events(ctx, after)
	if err != nil {
		return err
	}
	b.Health.Set("agents", "ok", "")
	for _, ev := range evs {
		b.Health.Seen("agents")
		if b.Limit == nil || b.Limit.Allow("agents") {
			if err := b.Sink.Ingest(FromBus(ev)); err != nil {
				return err
			}
		} else {
			b.Sink.Suppressed("agents", 1)
		}
		if err := b.Cursor.SetCursor("agents", strconv.FormatInt(ev.Seq, 10)); err != nil {
			return err
		}
	}
	return nil
}
