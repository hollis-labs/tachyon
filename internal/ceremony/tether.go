package ceremony

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TetherHistory reads Torque's checkpoint.state_changed events from Tether's bus (GET /events?kind=), newest
// first, one page (a month of checkpoints fits: 39 events on 2026-11-14). The query is slow (up to 71s seen), so
// serve it through CachedHistory.
type TetherHistory struct {
	Base   string
	Client *http.Client // builderread.NewSlowTetherClient: read token, Tether-Protocol header, long timeouts
	Limit  int
}

func (t *TetherHistory) CheckpointEvents(ctx context.Context) ([]CheckpointEvent, error) {
	limit := t.Limit
	if limit <= 0 {
		limit = 500
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/events?kind=checkpoint.state_changed&order=desc&limit=%d", t.Base, limit), nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tether unreachable")
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tether answered %d", resp.StatusCode)
	}
	var page struct {
		Events []struct {
			At      string          `json:"at"`
			Payload json.RawMessage `json:"payload_json"`
		} `json:"events"`
	}
	if json.Unmarshal(b, &page) != nil {
		return nil, fmt.Errorf("tether sent an unreadable answer")
	}
	var out []CheckpointEvent
	for _, e := range page.Events {
		raw := []byte(e.Payload)
		var inner string
		if json.Unmarshal(raw, &inner) == nil {
			raw = []byte(inner)
		}
		var p struct {
			Entity struct {
				ID string `json:"id"`
			} `json:"entity"`
			To string `json:"to"`
		}
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if json.Unmarshal(raw, &p) != nil || err != nil || p.Entity.ID == "" || p.To == "" {
			continue
		}
		out = append(out, CheckpointEvent{ID: p.Entity.ID, State: p.To, At: at.UTC()})
	}
	return out, nil
}
