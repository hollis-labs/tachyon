package alertsource

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

// TorqueNote is the health note while the Torque stream is up: it has no stable event ids or cursor (design
// request R1), so replays are folded by content (type, subject, updated_at) and a gap while disconnected is lost.
const TorqueNote = "Torque has no event ids yet; duplicates are folded by content."

// FromTorque normalizes one Torque domain event (type from the SSE `event:` line or data.type). Only task state
// changes and pending checkpoints become events; the rest of Torque's traffic is not alert material. Torque
// sends no ids, so SourceEventID stays empty and DedupeKey falls back to type|subject|updated_at.
func FromTorque(typ string, data []byte) (Event, bool) {
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return Event{}, false
	}
	if typ == "" {
		typ, _ = raw["type"].(string)
	}
	d := raw
	if inner, ok := raw["data"].(map[string]any); ok {
		d = inner
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := d[k].(string); ok && v != "" {
				return Clean(v, 300)
			}
		}
		return ""
	}
	id := str("task_id", "id")
	if id == "" || len(typ) > 80 {
		return Event{}, false
	}
	title := str("title", "name")
	label := title
	if label == "" {
		label = id
	}
	e := Event{Source: "torque", AtMS: time.Now().UnixMilli(), Payload: map[string]any{"title": Clean(title, 120)}}
	if v := str("updated_at", "revision"); v != "" {
		e.Payload["updated_at"] = v
	}
	switch {
	case strings.HasPrefix(typ, "task."):
		to := str("status", "to", "new_status")
		if to == "" {
			return Event{}, false
		}
		e.Kind = "task.state_changed"
		e.Payload["to"], e.Payload["from"] = to, str("from", "old_status", "previous_status")
		e.Subjects = []alertsapi.Subject{{Kind: "task", Ref: Clean(id, 200), Label: Clean(label, 120)}}
	case strings.HasPrefix(typ, "checkpoint."):
		if st := str("status", "state", "to"); st != "" && st != "pending" {
			return Event{}, false
		}
		e.Kind = "checkpoint.pending"
		e.Payload["question"] = str("question", "prompt")
		e.Subjects = []alertsapi.Subject{{Kind: "checkpoint", Ref: Clean(id, 200), Label: Clean(label, 120)}}
	default:
		return Event{}, false
	}
	return e, true
}

// Torque follows Torque's /api/v1/events stream. There is no resume cursor, so a reconnect starts live.
type Torque struct {
	URL    string // TACHYON torque base + "/api/v1/events"
	Token  string // bearer; may be empty
	Client *http.Client
	Sink   Sink
	Health *Health
	Limit  *Limiter
}

// Run is one connection; the supervisor reconnects.
func (t *Torque) Run(ctx context.Context) error {
	if err := upstreamauth.Guard(t.URL, t.Token); err != nil {
		return fmt.Errorf("torque stream refused: %w", err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}
	c := t.Client
	if c == nil {
		c = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("torque stream: unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("torque stream: HTTP %d", resp.StatusCode)
	}
	t.Health.Set("torque", "degraded", TorqueNote)
	return ReadSSE(resp.Body, func(f Frame) error {
		ev, ok := FromTorque(f.Event, []byte(f.Data))
		if !ok {
			return nil
		}
		t.Health.Seen("torque")
		if t.Limit == nil || t.Limit.Allow("torque") {
			return t.Sink.Ingest(ev)
		}
		t.Sink.Suppressed("torque", 1)
		return nil
	})
}
