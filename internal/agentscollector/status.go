package agentscollector

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentsapi"
)

// Status is the body of GET agents/collector (agentsapi openapi: Collector).
type Status struct {
	Collecting    bool                      `json:"collecting"`
	Sessions      int                       `json:"sessions"`
	LastEventAt   *int64                    `json:"last_event_at"`
	Strategy      string                    `json:"strategy"`
	Sources       map[string]map[string]any `json:"sources"`
	DroppedOutput int64                     `json:"dropped_output_frames"`
	OpenGaps      int                       `json:"open_gaps"`
	Retention     map[string]any            `json:"retention"`
	BusTopics     map[string]string         `json:"bus_topics"`
}

// Status reports whether collection is actually happening. "collecting" is true only while the
// stream is connected: an unreachable Tether is never shown as an empty healthy page.
func (c *Collector) Status(ctx context.Context) (Status, error) {
	st, err := c.Store.State(ctx)
	if err != nil {
		return Status{}, err
	}
	n, err := c.Store.SessionCount(ctx)
	if err != nil {
		return Status{}, err
	}
	gaps, err := c.Store.OpenGaps(ctx)
	if err != nil {
		return Status{}, err
	}
	c.mu.Lock()
	src := c.src
	c.mu.Unlock()
	if src.State == "" {
		src.State = "off"
	}
	out := Status{
		Collecting: c.streamed.Load(), Sessions: n, Strategy: map[string]string{"snapshot": "snapshot", "fallback": "fallback"}[st.Strategy],
		Sources:       map[string]map[string]any{"tether": {"state": src.State, "detail": src.Detail}},
		DroppedOutput: c.dropped.Load(), OpenGaps: gaps,
		Retention: map[string]any{"last_run_at": nilTime(st.RetentionLast), "rows_pruned": st.Pruned},
		// Which Activity topics can ever have data: the bus feed (A4) turns these on.
		BusTopics: map[string]string{"deploy": "off", "restart": "off", "task": "off", "failure": "off", "host": "no_emitter", "workstream": "no_emitter", "lifecycle": "off", "card": "off"},
	}
	if out.Strategy == "" {
		out.Strategy = "fallback"
	}
	if !st.LastEventAt.IsZero() {
		ms := st.LastEventAt.UnixMilli()
		out.LastEventAt = &ms
	}
	return out, nil
}

func nilTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

// Handler serves GET agents/collector. GETs pass RequireOperator without a session, like the rest
// of the agents reads; it exposes counts and states only.
func (c *Collector) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st, err := c.Status(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "internal", "message": "The request could not be completed."})
			return
		}
		_ = json.NewEncoder(w).Encode(st)
	})
}

// Path is the one route this package serves.
const Path = agentsapi.Prefix + "/collector"
