package alertsource

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/hollis-labs/tachyon/internal/agentscollector"
	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

// TetherHistory is the slice of the collector's Tether client the poll adapter needs.
type TetherHistory interface {
	Head(ctx context.Context) (int64, error)
	History(ctx context.Context, since, before int64, limit int) ([]agentscollector.Frame, int64, error)
}

// FromTether normalizes the Tether frames that are alert material: a session waiting on a permission or an
// answer (agent.state), and failed or dead-lettered messages. Everything else is the Agents collector's.
func FromTether(f agentscollector.Frame) (Event, bool) {
	e := Event{Source: "tether", SourceEventID: strconv.FormatInt(f.Seq, 10), AtMS: f.At.UnixMilli(), Payload: map[string]any{}}
	if f.SessionID != "" {
		e.Subjects = []alertsapi.Subject{{Kind: "session", Ref: Clean(f.SessionID, 200), Label: Clean(firstNonEmpty(f.AgentName, f.SessionID), 120)}}
	}
	switch {
	case f.WorkState == "waiting" || (f.PendingApproval != nil && *f.PendingApproval) || (f.PendingQuestion != nil && *f.PendingQuestion):
		e.Kind = "agent.state"
		wait := "an answer"
		if f.PendingApproval != nil && *f.PendingApproval {
			wait = "a permission"
		}
		e.Payload["state"], e.Payload["waitingFor"] = "waiting", "Waiting on "+wait
		e.Payload["agent"] = Clean(firstNonEmpty(f.AgentName, f.SessionID), 120)
	case strings.HasPrefix(f.Kind, "message.") && strings.Contains(f.Kind, "dead_letter"):
		e.Kind = "message.dead_lettered"
	case f.Kind == "message.wake_deferred":
		return Event{}, false // a deferral, not a failure
	case f.Kind == "message.failed":
		// Only terminal states are alert material; retry_scheduled is Tether doing its job.
		switch f.DeliveryState {
		case "retry_scheduled", "wake_deferred":
			return Event{}, false
		}
		e.Kind = "message.failed"
	default:
		return Event{}, false
	}
	if f.At.IsZero() {
		return Event{}, false
	}
	if strings.HasPrefix(e.Kind, "message.") {
		if f.DeliveryState != "" {
			e.Payload["state"] = f.DeliveryState
		}
		if f.Reason != "" {
			e.Payload["reason"] = f.Reason
		}
		if f.From != "" {
			e.Payload["from"] = f.From
		}
	}
	return e, true
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// TetherPoll reads Tether's event history after the stored sequence. The first run starts at the head: alerts
// before first ingest do not exist.
type TetherPoll struct {
	Src    TetherHistory
	Sink   Sink
	Cursor CursorStore
	Health *Health
	Limit  *Limiter
}

func (p *TetherPoll) Poll(ctx context.Context) error {
	cur, err := p.Cursor.Cursor("tether")
	if err != nil {
		return err
	}
	after, _ := strconv.ParseInt(cur, 10, 64)
	if cur == "" {
		head, err := p.Src.Head(ctx)
		if err != nil {
			return err
		}
		p.Health.Set("tether", "ok", "")
		return p.Cursor.SetCursor("tether", strconv.FormatInt(head, 10))
	}
	var frames []agentscollector.Frame
	before := int64(0)
	for page := 0; page < 3; page++ {
		fs, next, err := p.Src.History(ctx, after, before, 200)
		if err != nil {
			return err
		}
		frames = append(frames, fs...)
		if next == 0 || len(fs) == 0 {
			break
		}
		before = next
	}
	p.Health.Set("tether", "ok", "")
	sort.Slice(frames, func(i, j int) bool { return frames[i].Seq < frames[j].Seq })
	for _, f := range frames {
		if f.Seq <= after {
			continue
		}
		if ev, ok := FromTether(f); ok {
			p.Health.Seen("tether")
			if p.Limit == nil || p.Limit.Allow("tether") {
				if err := p.Sink.Ingest(ev); err != nil {
					return err
				}
			} else {
				p.Sink.Suppressed("tether", 1)
			}
		}
		after = f.Seq
		if err := p.Cursor.SetCursor("tether", strconv.FormatInt(after, 10)); err != nil {
			return err
		}
	}
	return nil
}
