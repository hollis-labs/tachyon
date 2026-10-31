package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/agentscollector"
	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsource"
	"github.com/hollis-labs/tachyon/internal/alertsprefs"
	"github.com/hollis-labs/tachyon/internal/alertsstream"
	"github.com/hollis-labs/tachyon/internal/alertstore"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const alertsFeedKey = "alerts.feed"

func init() {
	registerStart(startAlertsFeed)
	registerServerMount(mountAlertsFeed)
}

type alertsFeed struct {
	hub    *alertsstream.Hub
	health *alertsource.Health
	store  *alertstore.Store
	api    *alertstore.API
	sink   *alertSink
}

// alertChange maps the store's change names to the stream's frame types and toast reasons.
var alertChange = map[string]struct {
	typ string
	ch  alertsprefs.Change
}{
	"created":   {"alert.created", alertsprefs.Created},
	"updated":   {"alert.updated", alertsprefs.Raised},
	"state":     {"alert.state", alertsprefs.Settled},
	"woke":      {"alert.woke", alertsprefs.Woke},
	"escalated": {"alert.escalated", alertsprefs.Escalated},
}

// startAlertsFeed gives the Alerts store its consumers (CW-20261030-0131): the SSE hub fed by the store's
// OnChange (with the server-computed toast flag), the source health registry, and the ingest adapters that
// exist today: the activity monitor stream, the Agents bus feed, Torque's event stream and the Tether history
// poll (each only when its upstream is configured). TACHYON_ALERTS_SOURCES=0 leaves the adapters off (the stream still runs). It runs after
// startAlerts (filename order) and stands down with the alerts stub.
func startAlertsFeed(ctx context.Context, s *Server, db *localstore.Store) {
	v, ok := s.ext.Load(alertsKey)
	if alertsStubsOn() || !ok {
		return
	}
	api := v.(*alertstore.API)
	st := api.Store
	f := &alertsFeed{hub: alertsstream.NewHub(1000), health: alertsource.NewHealth(), store: st, api: api, sink: s.newAlertSink()}
	s.ext.Store(alertsFeedKey, f)
	st.OnChange = f.publish

	if os.Getenv("TACHYON_ALERTS_SOURCES") == "0" {
		return
	}
	go f.emitApprovals(ctx, db)
	limit := alertsource.NewLimiter(200)
	if base := strings.TrimRight(strings.TrimSpace(s.cfg.Upstreams.ActivityURL), "/"); base != "" {
		act := &alertsource.Activity{URL: base + "/api/stream", Sink: st, Cursor: st, Health: f.health, Limit: limit}
		go alertsource.Supervise(ctx, "activity", f.health, time.Minute, act.Run)
	}
	if base := strings.TrimRight(strings.TrimSpace(s.cfg.Upstreams.TorqueURL), "/"); base != "" {
		tq := &alertsource.Torque{URL: base + "/api/v1/events", Token: s.cfg.Upstreams.TorqueToken, Sink: st, Health: f.health, Limit: limit}
		go alertsource.Supervise(ctx, "torque", f.health, time.Minute, tq.Run)
	}
	if addr := strings.TrimSpace(s.cfg.Upstreams.TetherAddr); addr != "" {
		if src, err := agentscollector.NewHTTPSource(addr, s.cfg.Upstreams.TetherTokenFile); err != nil {
			slog.Warn("alerts: tether poll adapter not started", "err", err)
			f.health.Set("tether", "down", "Tether is not configured for alerts.")
		} else {
			poll := &alertsource.TetherPoll{Src: src, Sink: st, Cursor: st, Health: f.health, Limit: limit}
			go alertsource.Supervise(ctx, "tether", f.health, time.Minute, func(ctx context.Context) error {
				t := time.NewTicker(10 * time.Second)
				defer t.Stop()
				for {
					if err := poll.Poll(ctx); err != nil {
						return err
					}
					select {
					case <-ctx.Done():
						return nil
					case <-t.C:
					}
				}
			})
		}
	}
	bus := &alertsource.Bus{Src: busSource{db}, Sink: st, Cursor: st, Health: f.health, Limit: limit}
	go alertsource.Supervise(ctx, "agents", f.health, time.Minute, func(ctx context.Context) error {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			if err := bus.Poll(ctx); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
			}
		}
	})
}

// publish turns one committed store change into an alert frame plus a counts frame, both under the
// alert's seq so a Last-Event-ID resume replays them together.
func (f *alertsFeed) publish(id string, seq int64, change string) {
	m, ok := alertChange[change]
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := f.store.Get(ctx, id)
	if err != nil {
		return
	}
	now := time.Now()
	prefs, err := f.api.CurrentPrefs(ctx)
	if err != nil {
		prefs = alertsapi.DefaultPrefs("UTC")
	}
	toast := alertsprefs.ToastFor(prefs, m.ch, a.Severity, a.Source, now)
	if err := f.hub.Publish(seq, alertsstream.AlertFrame(m.typ, a, toast, "", now)); err != nil {
		slog.Warn("alerts: stream publish failed", "err", err)
		return
	}
	f.sink.notify(a, seq, toast)
	if c, err := f.store.Counts(ctx); err == nil {
		_ = f.hub.Publish(seq, alertsstream.CountsFrame(c, now))
	}
}

// busSource reads the collector's bus_events table for the bus adapter.
type busSource struct{ db *localstore.Store }

func (b busSource) Events(ctx context.Context, after int64) ([]agentscollector.BusEvent, error) {
	rows, err := b.db.Reader.QueryContext(ctx, `SELECT tether_seq,tether_event_id,at_ns,type,subject,actor,app,severity,refs_json,card_json FROM bus_events WHERE tether_seq>? ORDER BY tether_seq LIMIT 200`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []agentscollector.BusEvent
	for rows.Next() {
		var e agentscollector.BusEvent
		var ns int64
		var refs string
		var card sql.NullString
		if err := rows.Scan(&e.Seq, &e.EventID, &ns, &e.Type, &e.Subject, &e.Actor, &e.App, &e.Severity, &refs, &card); err != nil {
			return nil, err
		}
		e.At = time.Unix(0, ns).UTC()
		_ = json.Unmarshal([]byte(refs), &e.Refs)
		if card.Valid {
			var c agentscollector.BusCard
			if json.Unmarshal([]byte(card.String), &c) == nil {
				e.Card = &c
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// mountAlertsFeed mounts GET stream, GET sources and GET rules by explicit pattern (the /api/plugins/ catch-all is
// unguarded). Like the other alert routes they are for the operator's browser session only.
func mountAlertsFeed(s *Server, mux *http.ServeMux, _ *approvals.Bridge) {
	if alertsStubsOn() {
		return
	}
	v, ok := s.ext.Load(alertsFeedKey)
	if !ok {
		return
	}
	f := v.(*alertsFeed)
	stream := &alertsstream.Stream{Hub: f.hub, Counts: func() alertsapi.Counts {
		c, _ := f.store.Counts(context.Background())
		return c
	}}
	sources := alertsstream.Sources(func() []alertsstream.SourceState {
		snap := f.health.Snapshot()
		out := make([]alertsstream.SourceState, 0, len(snap))
		for _, h := range snap {
			out = append(out, alertsstream.SourceState(h))
		}
		return out
	}, nil)
	mux.Handle("GET "+alertsapi.Prefix+"/stream", noAuthHeader(stream))
	mux.Handle("GET "+alertsapi.Prefix+"/sources", noAuthHeader(sources))
	mux.Handle("GET "+alertsapi.Prefix+"/rules", noAuthHeader(alertsstream.Rules(f.store.Pack)))
}

// noAuthHeader refuses agent bearer calls, as the rest of the alert surface does.
func noAuthHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": alertsapi.CodeForbidden, "message": "Alerts are available to the operator's browser session only."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// emitApprovals raises an approval.requested alert for each new queue item, with the item as an "approval"
// subject: the detail links to it and FollowApprovals settles it when it is decided or expires. The alert
// carries the item's title and summary only. Items created while Tachyon was down are not replayed.
func (f *alertsFeed) emitApprovals(ctx context.Context, db *localstore.Store) {
	feed := approvalFeed{approvals.Changes(db)}
	store := &approvals.Store{DB: db}
	for ctx.Err() == nil {
		ch, cancel := feed.Subscribe()
	loop:
		for {
			select {
			case <-ctx.Done():
				cancel()
				return
			case c, ok := <-ch:
				if !ok {
					break loop
				}
				if c.Type != "item.created" {
					continue
				}
				rec, err := store.Get(ctx, c.ItemID)
				if err != nil {
					continue
				}
				p := approvals.Project(rec, time.Now(), nil)
				ev := alertsource.Event{Source: "approvals", SourceEventID: "created:" + c.ItemID, Kind: "approval.requested", AtMS: time.Now().UnixMilli(),
					Subjects: []alertsapi.Subject{{Kind: alertstore.ApprovalSubject, Ref: c.ItemID, Label: alertsource.Clean(p.Title, 120)}},
					Payload:  map[string]any{"title": alertsource.Clean(p.Title, 120), "summary": alertsource.Clean(p.Summary, 300)}}
				if err := f.store.Ingest(ev); err != nil {
					slog.Warn("alerts: approval alert failed", "err", err)
				}
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
