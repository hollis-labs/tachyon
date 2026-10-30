package alertsstream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

// Stream serves GET /alerts/stream. The caller mounts it behind the route guards; it checks nothing itself.
type Stream struct {
	Hub *Hub
	// Heartbeat is the comment interval (default 20 s).
	Heartbeat time.Duration
	// Counts, when set, is sent as the first frame of a fresh connection (no cursor) so the badge is right
	// without a second request. It carries no id.
	Counts func() alertsapi.Counts
	Now    func() time.Time
}

func (s *Stream) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Stream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var after int64
	hasCursor := false
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	if v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, 400, alertsapi.CodeInvalid, "Last-Event-ID must be a non-negative integer.")
			return
		}
		after, hasCursor = n, true
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "internal", "streaming unsupported")
		return
	}
	// Subscribe before replaying so nothing published in between is lost; the serial check drops duplicates.
	sub := s.Hub.Subscribe()
	defer sub.Close()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)

	var sent uint64
	send := func(f Frame) bool {
		if f.n <= sent && f.n != 0 {
			return true
		}
		if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", f.ID, f.Data); err != nil {
			return false
		}
		if f.n > sent {
			sent = f.n
		}
		return true
	}
	if hasCursor {
		frames, ok := s.Hub.Replay(after)
		if !ok {
			body, _ := json.Marshal(ResyncFrame("gap", s.now()))
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", s.Hub.Top(), body)
			fl.Flush()
			// Live frames still flow after the resync; the client refetches the list and carries on.
			sent = sub.start
		}
		for _, f := range frames {
			if !send(f) {
				return
			}
		}
	} else {
		sent = sub.start
		if s.Counts != nil {
			body, _ := json.Marshal(CountsFrame(s.Counts(), s.now()))
			fmt.Fprintf(w, "data: %s\n\n", body)
		}
	}
	fl.Flush()
	if r.URL.Query().Get("once") == "1" {
		return
	}
	hb := s.Heartbeat
	if hb <= 0 {
		hb = 20 * time.Second
	}
	t := time.NewTicker(hb)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case f, open := <-sub.C:
			if !open {
				return // too slow: the client reconnects with Last-Event-ID
			}
			if !send(f) {
				return
			}
			fl.Flush()
		case <-t.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

// SourceState is one ingest source's health. It has the same fields as alertsource.SourceHealth, so the
// ingest registry's snapshot converts directly: []SourceState(...) per element.
type SourceState struct {
	Source      string `json:"source"`
	State       string `json:"state"` // ok | degraded | down
	LastEventMS int64  `json:"last_event_ms"`
	Note        string `json:"note"`
}

// Sources serves GET /alerts/sources from a snapshot function. A source that never reported is absent. Notes
// are operator-facing text from the adapters, never raw upstream errors.
func Sources(snapshot func() []SourceState, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		list := snapshot()
		if list == nil {
			list = []SourceState{}
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sources": list, "server_now_ms": now().UnixMilli()})
	})
}
