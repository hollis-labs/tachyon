package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsource"
	"github.com/hollis-labs/tachyon/internal/alertstore"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/builderread"
	"github.com/hollis-labs/tachyon/internal/ceremony"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/needsyou"
)

// CeremonyPath is GET ceremony (CW-20261114-0023f): the ceremony meter.
const CeremonyPath = "/api/plugins/console/v1/ceremony"

const ceremonyKey = "ceremony.meter"

// ceremonyCheckEvery is how often the meter is read in the background: it keeps the daily snapshot current and
// raises the regression alert.
var ceremonyCheckEvery = time.Hour

// ceremonyMaxAge is how old a read GET ceremony may serve before it reads again.
const ceremonyMaxAge = time.Minute

func init() {
	registerStart(startCeremony)
	registerServerMount(mountCeremony)
}

func startCeremony(ctx context.Context, s *Server, store *localstore.Store) {
	m := &ceremony.Meterer{DB: store}
	if u := strings.TrimSpace(s.cfg.Upstreams.TorqueURL); u != "" {
		m.Torque = &ceremony.TorqueHTTP{Base: u, Token: s.cfg.Upstreams.TorqueToken}
	}
	// Asks to @chrispian come from his mailbox (a normal, fast read). Checkpoints come from Torque and the
	// collector's own stream: the meter runs no Tether event-history query (an unindexed scan on the hub).
	if addr := strings.TrimSpace(s.cfg.Upstreams.TetherAddr); addr != "" && s.cfg.Upstreams.TetherTokenFile != "" {
		if base, c, err := builderread.NewTetherClient(addr, s.cfg.Upstreams.TetherTokenFile); err == nil {
			m.Asks = &needsyou.Aggregator{Tether: &needsyou.HTTPTether{Base: base, Client: c}}
		}
	}
	s.ext.Store(ceremonyKey, m)
	go s.watchCeremony(ctx, m)
}

func mountCeremony(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	mux.Handle("GET "+CeremonyPath, operator.RequireOperator(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.ext.Load(ceremonyKey)
		if !ok {
			http.Error(w, `{"code":"unavailable","message":"The ceremony meter has not started."}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(v.(*ceremony.Meterer).Latest(r.Context(), ceremonyMaxAge))
	})))
}

// watchCeremony reads the meter hourly (keeping today's snapshot) and raises one warning per week when activity
// rose 25% or more while tasks closed done did not.
func (s *Server) watchCeremony(ctx context.Context, m *ceremony.Meterer) {
	t := time.NewTicker(ceremonyCheckEvery)
	defer t.Stop()
	for {
		s.checkCeremony(ctx, m)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) checkCeremony(ctx context.Context, m *ceremony.Meterer) {
	read := m.Read(ctx)
	if read.Comparison != "regression" || read.Previous == nil {
		return
	}
	v, ok := s.ext.Load(alertsKey)
	if !ok {
		return
	}
	year, week := read.CheckedAt.ISOWeek()
	err := v.(*alertstore.API).Store.Ingest(alertsource.Event{
		Source: "ceremony", SourceEventID: fmt.Sprintf("regression:%d-w%02d", year, week),
		Kind: "ceremony.regression", AtMS: read.CheckedAt.UnixMilli(), SeverityHint: "warning",
		Subjects:    []alertsapi.Subject{{Kind: "service", Ref: "ceremony", Label: "Ceremony meter"}},
		CoalesceKey: "ceremony",
		Payload: map[string]any{"activity": read.Activity, "activity_prev": read.Previous.Activity, "done": read.Done, "done_prev": read.Previous.Done,
			"title": "Activity rose while progress did not"},
	})
	if err != nil {
		slog.Warn("ceremony: alert ingest failed", "err", err)
	}
}
