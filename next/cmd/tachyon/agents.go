package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/agentsapi"
	"github.com/hollis-labs/tachyon/next/internal/agentsassign"
	"github.com/hollis-labs/tachyon/next/internal/agentscollector"
	"github.com/hollis-labs/tachyon/next/internal/agentsserve"
	"github.com/hollis-labs/tachyon/next/internal/approvals"
	"github.com/hollis-labs/tachyon/next/internal/ceremony"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatthreads"
	"github.com/hollis-labs/tachyon/next/internal/needsyou"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/planner"
	"github.com/hollis-labs/tachyon/next/internal/plannerassign"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// The agents domain, read side (slice B8a): Agent Ops (sessions, timeline, series, tokens, history,
// stream, settings, seats, control status, collector status, the bus feed), the ceremony meter and
// Needs you, all from the migrated tables. The collector is built but not started, so nothing is
// collected; its loop, the controls (nudge, restart, skip, settings writes) and the Tether launch
// passthroughs are slice B8b, behind switches that default off.

// ceremonyMaxAge is how old a read GET ceremony may serve before it reads again (the old build's value).
const ceremonyMaxAge = time.Minute

// ceremonyPath is GET ceremony, the ceremony meter.
const ceremonyPath = "/api/plugins/console/v1/ceremony"

type agentsDeps struct {
	collector *agentscollector.Collector
	server    *agentsserve.Server
	needsYou  http.Handler
	ceremony  http.Handler
}

// newAgents builds the read side as the old server did, without starting anything. Outbound reads
// happen only on demand and only to configured upstreams: Needs you reads the operator's mailbox from
// Tether, the ceremony meter reads Torque, and a session's assignment joins its Torque task.
func newAgents(app *module.App, cfg settings.Settings, domain *store.Store, chat chatDeps) agentsDeps {
	resolver := secrets.Resolver(app)
	cstore := &agentscollector.Store{DB: domain}
	d := agentsDeps{collector: &agentscollector.Collector{Store: cstore}}
	torque := strings.TrimRight(strings.TrimSpace(cfg.Upstreams.Torque.URL), "/")
	torqueToken := upstreamauth.FromRef(resolver, cfg.Upstreams.Torque.Token)
	d.server = &agentsserve.Server{DB: domain, Collector: d.collector,
		Worker: &plannerWork{svc: plannerassign.New(objects.New(domain), torque)}}
	if torque != "" {
		d.server.Assigner = &agentsassign.Joiner{DB: domain, Async: true, Planner: agentsassign.Live{
			Owned: &planner.Owned{Objects: objects.New(domain)},
			Tasks: planner.NewTasks(torque, torqueToken),
		}}
	}
	// Needs you: the mailbox asks (Tether, with the read credential), the seats' declared statuses, the
	// chat threads' open asks and the approvals queue.
	var asks needsyou.Tether
	if cfg.Upstreams.Tether.Token != "" {
		if base, client, err := tetherReadClient(cfg.Upstreams.Tether.URL, upstreamauth.FromRef(resolver, cfg.Upstreams.Tether.Token)); err == nil {
			asks = &needsyou.HTTPTether{Base: base, Client: client}
		}
	}
	ny := &needsyou.Aggregator{Tether: asks, Statuses: cstore, Approvals: approvalQueue{domain}}
	if chat.threads != nil && chat.threads.Agg != nil {
		ny.Chats = chatAsks{chat.threads.Agg}
	}
	// Reads need no operator, as the old RequireOperator let GETs through.
	d.needsYou = ny.Handler()
	// The ceremony meter: Torque for tasks closed, the mailbox for asks. Its hourly background read and
	// regression alert come with B8b; a GET reads again when the last read is over a minute old.
	m := &ceremony.Meterer{DB: domain}
	if torque != "" {
		if client, err := upstreamClient(torque, torqueToken); err == nil {
			m.Torque = &ceremony.TorqueHTTP{Base: torque, Client: client}
		}
	}
	if asks != nil {
		m.Asks = &needsyou.Aggregator{Tether: asks}
	}
	d.ceremony = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(m.Latest(r.Context(), ceremonyMaxAge))
	})
	return d
}

// agentsReadRoutes are the agents read routes; B8b adds the writes (PUT settings, nudge, restart, skip).
var agentsReadRoutes = []string{
	"/sessions", "/sessions/{id}", "/sessions/{id}/events", "/sessions/{id}/context-series", "/sessions/{id}/tokens",
	"/sessions/{id}/history", "/stream", "/settings", "/seats", "/controls/{request_id}",
}

// agentsRoutes are the agents routes, each under tachyon.agents.read (docs/api-contract.md, 5e).
func agentsRoutes(d agentsDeps) []routes.Route {
	if d.server == nil {
		return nil
	}
	// agentsserve mounts its own handlers; only the read routes reach this mux.
	mux := http.NewServeMux()
	d.server.Mount(mux, func(h http.Handler) http.Handler { return h })
	const read = "tachyon.agents.read"
	var rs []routes.Route
	for _, p := range agentsReadRoutes {
		rs = append(rs, routes.Route{Method: http.MethodGet, Pattern: agentsapi.Prefix + p, Scope: read, Handler: mux})
	}
	return append(rs,
		routes.Route{Method: http.MethodGet, Pattern: agentscollector.Path, Scope: read, Handler: d.collector.Handler()},
		routes.Route{Method: http.MethodGet, Pattern: agentscollector.BusPath, Scope: read, Handler: d.collector.BusHandler()},
		routes.Route{Method: http.MethodGet, Pattern: ceremonyPath, Scope: read, Handler: d.ceremony},
		routes.Route{Method: http.MethodGet, Pattern: needsyou.Path, Scope: read, Handler: d.needsYou},
	)
}

// tetherReadClient is the old builderread client for Tether reads: the configured address (https, a
// unix: socket, or tcp: host:port), the credential, Tether-Protocol 1, 20 s, no redirects.
func tetherReadClient(addr string, token upstreamauth.Token) (string, *http.Client, error) {
	if strings.TrimSpace(addr) == "" {
		return "", nil, errors.New("tether address is missing")
	}
	base := strings.TrimRight(addr, "/")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 15 * time.Second
	if socket, ok := strings.CutPrefix(addr, "unix:"); ok {
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}
		base = "http://localhost"
	} else if rest, ok := strings.CutPrefix(addr, "tcp:"); ok {
		base = "http://" + rest
	}
	rt, err := upstreamauth.Wrap(tr, base, token)
	if err != nil {
		return "", nil, err
	}
	return base, &http.Client{Transport: tetherProtocol{rt}, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

type tetherProtocol struct{ http.RoundTripper }

func (t tetherProtocol) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Tether-Protocol", "1")
	return t.RoundTripper.RoundTrip(r)
}

// upstreamClient is a plain client that carries the credential for base (refused over http to a
// non-loopback host).
func upstreamClient(base string, token upstreamauth.Token) (*http.Client, error) {
	rt, err := upstreamauth.Wrap(http.DefaultTransport, base, token)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt, Timeout: 20 * time.Second}, nil
}

// plannerWork reads every active assignment once and serves it per agent for 30 s, so a sessions list of
// any size costs one read (the old server's agentsWorker).
type plannerWork struct {
	svc *plannerassign.Service
	mu  sync.Mutex
	at  time.Time
	by  map[string][2]string // agent id -> {task ref, theme ref}
}

func (p *plannerWork) Work(ctx context.Context, agentID string) (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.by == nil || time.Since(p.at) > 30*time.Second {
		if views, err := p.svc.List(ctx, plannerassign.Filter{State: plannerassign.StateActive}); err == nil {
			p.by = plannerWorkIndex(views)
		} else if p.by == nil {
			p.by = map[string][2]string{}
		}
		p.at = time.Now()
	}
	w := p.by[agentID]
	return w[0], w[1]
}

// plannerWorkIndex keeps, per agent, the earliest assigned task and the earliest assigned theme (List
// returns assignments earliest first). Team assignments and other target kinds are left out.
func plannerWorkIndex(views []plannerassign.View) map[string][2]string {
	by := map[string][2]string{}
	for _, v := range views {
		if v.AgentID == "" {
			continue
		}
		w := by[v.AgentID]
		switch {
		case v.Target.Ref != "" && w[0] == "":
			w[0] = v.Target.Ref
		case v.Target.DB == "themes" && v.Target.ID != "" && w[1] == "":
			w[1] = "planner:theme/" + v.Target.ID
		}
		by[v.AgentID] = w
	}
	return by
}

// chatAsks reads the chat threads' open asks (blocked_on_user), archived threads excluded.
type chatAsks struct{ agg *chatthreads.Aggregator }

func (c chatAsks) OpenAsks(ctx context.Context) ([]needsyou.ChatAsk, error) {
	all, err := c.agg.Summaries(ctx)
	if err != nil {
		return nil, err
	}
	var out []needsyou.ChatAsk
	for _, t := range all {
		if t.Archived || t.BlockedOnUser == 0 {
			continue
		}
		pending := t.BlockedBreakdown.Approvals
		out = append(out, needsyou.ChatAsk{Ref: t.Ref, Title: t.Title, AgentID: t.Agent.AgentID, Agent: t.Agent.Label,
			Questions: t.BlockedOnUser - pending, Approvals: pending, LastAt: time.UnixMilli(t.LastAt).UTC()})
	}
	return out, nil
}

// approvalQueue reads the approvals queue: pending, not deferred.
type approvalQueue struct{ db *store.Store }

func (q approvalQueue) Waiting(ctx context.Context, now time.Time) (needsyou.ApprovalQueue, error) {
	st := &approvals.Store{DB: q.db}
	var out needsyou.ApprovalQueue
	// Counts cover the whole pending set; the pages (at most 100, newest first) are walked only to find the oldest.
	f := approvals.Filter{Limit: 100, Order: "created"}
	var last string
	for page := 0; page < 50; page++ {
		ids, next, counts, err := st.Pending(ctx, f, now)
		if err != nil {
			return out, err
		}
		if page == 0 {
			pending, _ := counts["pending"].(int)
			deferred, _ := counts["deferred"].(int)
			out.Pending = max(pending-deferred, 0)
			if byRisk, ok := counts["by_risk"].(map[string]int); ok {
				out.High = byRisk["high"]
			}
		}
		if len(ids) > 0 {
			last = ids[len(ids)-1]
		}
		if next == "" {
			break
		}
		f.After = next
	}
	if last != "" {
		if rec, err := st.Get(ctx, last); err == nil {
			out.Oldest = rec.CreatedAt.UTC()
		}
	}
	return out, nil
}
