package agentscollector

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// Tether publishes no fleet.node.* or workstream.created events, so the feed's host and workstream topics had no
// emitter. FleetEmitter polls Tether's /workers and /workstreams and turns changes into the same frames the
// collector already maps (busType), so they flow through Store.Apply and the SSE stream like any bus event.

// FleetWorker is the part of GET /workers the emitter reads.
type FleetWorker struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Connected bool   `json:"connected"`
}

// FleetWorkstream is the part of GET /workstreams the emitter reads.
type FleetWorkstream struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// FleetSource is implemented by sources that can list workers and workstreams.
type FleetSource interface {
	Fleet(ctx context.Context) ([]FleetWorker, []FleetWorkstream, error)
}

func (s *httpSource) Fleet(ctx context.Context) ([]FleetWorker, []FleetWorkstream, error) {
	var w struct {
		Workers []FleetWorker `json:"workers"`
	}
	if err := s.get(ctx, "/workers", nil, 4<<20, &w); err != nil {
		return nil, nil, err
	}
	var ws struct {
		Workstreams []FleetWorkstream `json:"workstreams"`
	}
	if err := s.get(ctx, "/workstreams", nil, 4<<20, &ws); err != nil {
		return nil, nil, err
	}
	return w.Workers, ws.Workstreams, nil
}

// FleetEmitter turns worker connect/disconnect and new workstreams into bus events.
type FleetEmitter struct {
	Source FleetSource
	Store  *Store
	Every  time.Duration // default 30 s
	// SeedWindow is how far back the first poll announces workstreams that already exist (default 7 days).
	SeedWindow time.Duration
	Now        func() time.Time

	hosts map[string]bool // worker id -> connected, nil until the first poll
	known map[string]bool // workstream ids already announced
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._:/@? -]+`)

// busName makes free text pass the feed's name check (okName) instead of collapsing to "unknown".
func busName(s string) string {
	s = strings.Join(strings.Fields(unsafeName.ReplaceAllString(s, " ")), " ")
	return clean(s, 80)
}

func (e *FleetEmitter) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Poll runs one pass and writes any new events. The first pass seeds host state without announcing it.
func (e *FleetEmitter) Poll(ctx context.Context) error {
	workers, streams, err := e.Source.Fleet(ctx)
	if err != nil {
		return err
	}
	now := e.now()
	window := e.SeedWindow
	if window <= 0 {
		window = 7 * 24 * time.Hour
	}
	first := e.hosts == nil
	if first {
		e.hosts, e.known = map[string]bool{}, map[string]bool{}
	}
	seq := int64(0)
	if st, err := e.Store.State(ctx); err == nil {
		seq = st.LastSeq // sorts the event where it happened among Tether's own
	}
	var frames []Frame
	for _, w := range workers {
		prev, seen := e.hosts[w.ID]
		e.hosts[w.ID] = w.Connected
		if first || (seen && prev == w.Connected) || w.ID == "" {
			continue
		}
		kind, word := "fleet.node.offline", "offline"
		if w.Connected {
			kind, word = "fleet.node.online", "online"
		}
		frames = append(frames, Frame{Kind: kind, App: "tether", Seq: seq, At: now, Name: busName(firstNonEmpty(w.Label, w.ID)),
			EventID: "fleet:" + w.ID + ":" + word + ":" + now.UTC().Format("20060102T150405")})
	}
	for _, s := range streams {
		if s.ID == "" || e.known[s.ID] {
			continue
		}
		e.known[s.ID] = true
		at, perr := time.Parse(time.RFC3339Nano, s.CreatedAt)
		if perr != nil {
			at = now
		}
		if first && now.Sub(at) > window {
			continue
		}
		frames = append(frames, Frame{Kind: "workstream.created", App: "tether", Seq: seq, At: at, Name: busName(firstNonEmpty(s.Name, s.ID)),
			EventID: "workstream-created:" + s.ID})
	}
	return e.Store.Apply(ctx, frames)
}

// Run polls until ctx ends. A failed poll is logged and retried on the next tick.
func (e *FleetEmitter) Run(ctx context.Context) {
	every := e.Every
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := e.Poll(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("agents fleet emitter: poll failed", "err", errText(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
