package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	libsecrets "github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/agentsapi"
	"github.com/hollis-labs/tachyon/next/internal/agentsassign"
	"github.com/hollis-labs/tachyon/next/internal/agentscollector"
	"github.com/hollis-labs/tachyon/next/internal/agentsderive"
	"github.com/hollis-labs/tachyon/next/internal/agentsserve"
	"github.com/hollis-labs/tachyon/next/internal/approvals"
	"github.com/hollis-labs/tachyon/next/internal/assurance"
	"github.com/hollis-labs/tachyon/next/internal/ceremony"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatthreads"
	"github.com/hollis-labs/tachyon/next/internal/needsyou"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/planner"
	"github.com/hollis-labs/tachyon/next/internal/plannerassign"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/tetherpass"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// The agents domain. Reads (slice B8a): Agent Ops (sessions, timeline, series, tokens, history, stream,
// settings, seats, control status, collector status, the bus feed), the ceremony meter and Needs you,
// from the migrated tables. Writes and outbound (slice B8b): the collector loop and the ceremony meter's
// hourly read behind tachyon.features.agents_collector; nudge, interrupt, restart and the Tether launches
// behind tachyon.features.agents_controls; settings writes; the Tether reads the launch dialog uses.

// ceremonyMaxAge is how old a read GET ceremony may serve before it reads again (the old build's value).
const ceremonyMaxAge = time.Minute

// ceremonyPath is GET ceremony, the ceremony meter.
const ceremonyPath = "/api/plugins/console/v1/ceremony"

// ceremonyCheckEvery is how often the meter is read in the background (the old build's value).
const ceremonyCheckEvery = time.Hour

// agentsControlAction is the assurance action of every agents write: an operator, as the old
// RequireOperator required for writes.
const agentsControlAction assurance.Action = "agents.control"

// The Tether operations the launch dialog uses (the old console plugin's proxy).
const (
	tetherPrefix   = "/api/plugins/console/v1/tether"
	launchesPath   = "/api/plugins/console/v1/catalog/launches"
	tetherIDFormat = `^[a-zA-Z0-9_-]{1,128}$`
)

type agentsDeps struct {
	collector *agentscollector.Collector
	server    *agentsserve.Server
	needsYou  http.Handler
	ceremony  http.Handler
	meter     *ceremony.Meterer
	tether    *tetherpass.Proxy
	// launches is tachyon.features.agents_controls: off, the Tether launches answer 503 disabled.
	launches bool
	start    func(ctx context.Context) (stop func())
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
	d.meter = m
	d.ceremony = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(m.Latest(r.Context(), ceremonyMaxAge))
	})
	d.tether = tetherpass.New(cfg.Upstreams.Tether.URL, upstreamauth.FromRef(resolver, cfg.Upstreams.Tether.Token))
	d.launches = cfg.Features.AgentsControls
	// Controls: nudges go as Tether mail with the messages credential, restarts use the control
	// credential, an interrupt replies through the chat bridge. Off, each capability reads unavailable.
	if cfg.Features.AgentsControls {
		if cfg.Upstreams.TetherMessages.Token != "" {
			if base, client, err := tetherReadClient(cfg.Upstreams.Tether.URL, upstreamauth.FromRef(resolver, cfg.Upstreams.TetherMessages.Token)); err == nil {
				d.server.Sender = &tetherMail{base: base, client: client}
			}
		}
		if cfg.Upstreams.TetherControl.Token != "" {
			addr := cfg.Upstreams.TetherControl.URL
			if addr == "" {
				addr = cfg.Upstreams.Tether.URL
			}
			if base, client, err := tetherControlClient(addr, upstreamauth.FromRef(resolver, cfg.Upstreams.TetherControl.Token)); err == nil {
				d.server.Restarter = &agentsserve.TetherRestarter{Base: base, Client: client}
			}
		}
		if chat.turns != nil && chat.turns.ConfigError == nil {
			d.server.Interrupter = chat.turns
		}
	}
	d.start = func(ctx context.Context) func() { return startAgents(ctx, cfg, resolver, d) }
	return d
}

// startAgents runs the collector and the ceremony meter's hourly read when
// tachyon.features.agents_collector is on and Tether is configured. It returns a function that stops
// them. Off, nothing runs and nothing is dialed.
func startAgents(ctx context.Context, cfg settings.Settings, resolver *libsecrets.Resolver, d agentsDeps) func() {
	if !cfg.Features.AgentsCollector || strings.TrimSpace(cfg.Upstreams.Tether.URL) == "" {
		return func() {}
	}
	src, err := agentscollector.NewHTTPSource(cfg.Upstreams.Tether.URL, upstreamauth.FromRef(resolver, cfg.Upstreams.Tether.Token))
	if err != nil {
		slog.Warn("agents collector is enabled but not configured; staying off", "err", err)
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	cstore := d.collector.Store
	// Output payloads are reduced in memory by the derivation sink and never stored.
	live := &agentsderive.Live{Save: func(ctx context.Context, id string, s agentsderive.Saved) error {
		return cstore.SetDerived(ctx, id, agentscollector.Derived{OneLiner: s.OneLiner, CallsInTurn: s.CallsInTurn, TurnStartedAt: s.TurnStartedAt,
			CallsDelta: s.CallsDelta, Tools: s.Tools, Seq: s.Seq, TokDelta: tokDelta(s.TokDelta), At: s.At,
			CtxUsed: s.Meter.Used, CtxWindow: s.Meter.Window, CtxModel: s.Meter.Model, CtxSeen: s.Meter.Seen, Compactions: s.Meter.Compactions, LastClear: s.Meter.LastClear})
	}}
	d.collector.Source, d.collector.Sink, d.collector.TurnEnded = src, live, live.EndTurn
	d.collector.Start(ctx)
	var wg sync.WaitGroup
	// Tether emits no fleet.node.* or workstream.created events; they are polled for the host and workstream topics.
	if fs, ok := src.(agentscollector.FleetSource); ok {
		wg.Add(1)
		go func() { defer wg.Done(); (&agentscollector.FleetEmitter{Source: fs, Store: cstore}).Run(ctx) }()
	}
	// The ceremony meter's hourly read keeps today's snapshot. Its regression alert goes to the alerts
	// slice's store, which is not ported yet.
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(ceremonyCheckEvery)
		defer t.Stop()
		for {
			_ = d.meter.Read(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return func() {
		cancel()
		d.collector.Wait(context.Background())
		wg.Wait()
	}
}

func tokDelta(u *agentsderive.Usage) *[4]int64 {
	if u == nil {
		return nil
	}
	return &[4]int64{u.Input, u.Output, u.CacheRead, u.CacheWrite}
}

// agentsReadRoutes are the agents read routes.
var agentsReadRoutes = []string{
	"/sessions", "/sessions/{id}", "/sessions/{id}/events", "/sessions/{id}/context-series", "/sessions/{id}/tokens",
	"/sessions/{id}/history", "/stream", "/settings", "/seats", "/controls/{request_id}",
}

// agentsWriteRoutes are the agents writes: each needs tachyon.agents.control and an operator.
var agentsWriteRoutes = [][2]string{
	{http.MethodPut, "/settings"}, {http.MethodPost, "/sessions/{id}/nudge"}, {http.MethodPost, "/sessions/{id}/restart"},
	{http.MethodPost, "/controls/{request_id}/skip"},
}

// agentsRoutes are the agents routes: reads under tachyon.agents.read, writes under
// tachyon.agents.control with the operator check (docs/api-contract.md, 5e).
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
	const control = "tachyon.agents.control"
	operator := func(h http.Handler) http.Handler { return assurance.Middleware(agentsControlAction, h) }
	for _, w := range agentsWriteRoutes {
		rs = append(rs, routes.Route{Method: w[0], Pattern: agentsapi.Prefix + w[1], Scope: control, Handler: operator(mux)})
	}
	launch := func(h http.Handler) http.Handler {
		if d.launches {
			return operator(h)
		}
		return operator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "disabled", "message": "Launching agents is off (tachyon.features.agents_controls); nothing was sent"})
		}))
	}
	id := regexp.MustCompile(tetherIDFormat)
	sessionPath := func(suffix string) func(r *http.Request) (string, url.Values, error) {
		return func(r *http.Request) (string, url.Values, error) {
			sid := r.PathValue("id")
			if !id.MatchString(sid) {
				return "", nil, tetherpass.ErrUnknown // no reviewed operation matches, as the old proxy answered
			}
			return "/sessions/" + sid + suffix, nil, nil
		}
	}
	fixed := func(path string, q url.Values) func(*http.Request) (string, url.Values, error) {
		return func(*http.Request) (string, url.Values, error) { return path, q, nil }
	}
	rs = append(rs,
		routes.Route{Method: http.MethodGet, Pattern: tetherPrefix + "/workers", Scope: read, Handler: d.tether.Handler(tetherpass.Op{Target: fixed("/workers", nil)})},
		routes.Route{Method: http.MethodGet, Pattern: tetherPrefix + "/sessions", Scope: read, Handler: d.tether.Handler(tetherpass.Op{Target: func(r *http.Request) (string, url.Values, error) {
			q := url.Values{"state": {"running"}, "limit": {"1000"}}
			if c := r.URL.Query().Get("cursor"); c != "" {
				q.Set("cursor", c)
			}
			return "/sessions", q, nil
		}})},
		routes.Route{Method: http.MethodGet, Pattern: tetherPrefix + "/sessions/{id}", Scope: read, Handler: d.tether.Handler(tetherpass.Op{Target: sessionPath("")})},
		routes.Route{Method: http.MethodPost, Pattern: tetherPrefix + "/sessions", Scope: control,
			Handler: launch(d.tether.Handler(tetherpass.Op{Target: fixed("/sessions", nil), Fields: "launch worker prompt_append idempotency_key"}))},
		routes.Route{Method: http.MethodPost, Pattern: tetherPrefix + "/sessions/{id}/launch", Scope: control,
			Handler: launch(d.tether.Handler(tetherpass.Op{Target: sessionPath("/launch"), Fields: tetherpass.Empty}))},
		routes.Route{Method: http.MethodGet, Pattern: launchesPath, Scope: read, Handler: d.tether.Handler(tetherpass.Op{Target: func(r *http.Request) (string, url.Values, error) {
			return "/catalog/launches", r.URL.Query(), nil
		}})},
	)
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
	return tetherClient(addr, token, 15*time.Second, 20*time.Second)
}

// tetherControlClient is the old builderchange control client: the control credential, 30 s for the
// headers, 60 s overall.
func tetherControlClient(addr string, token upstreamauth.Token) (string, *http.Client, error) {
	return tetherClient(addr, token, 30*time.Second, 60*time.Second)
}

func tetherClient(addr string, token upstreamauth.Token, headerTimeout, timeout time.Duration) (string, *http.Client, error) {
	if strings.TrimSpace(addr) == "" {
		return "", nil, errors.New("tether address is missing")
	}
	base := strings.TrimRight(addr, "/")
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = headerTimeout
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
	return base, &http.Client{Transport: tetherProtocol{rt}, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

type tetherProtocol struct{ http.RoundTripper }

func (t tetherProtocol) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Tether-Protocol", "1")
	return t.RoundTripper.RoundTrip(r)
}

// tetherMail sends a nudge as Tether mail (the old forgeassign.TetherMail).
type tetherMail struct {
	base   string
	client *http.Client
}

func (t *tetherMail) Send(ctx context.Context, to, subject, body string, meta map[string]string) (string, error) {
	md := map[string]any{"subject": subject}
	for k, v := range meta {
		md[k] = v
	}
	b, _ := json.Marshal(map[string]any{"kind": "notice", "to": to, "payload": map[string]any{"subject": subject, "body": body}, "metadata": md})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/messages", bytes.NewReader(b))
	if err != nil {
		return "", errors.New("Tether could not be reached") //nolint:staticcheck // a product name; operator-visible text kept
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req) //nolint:gosec // the configured Tether address
	if err != nil {
		return "", errors.New("Tether could not be reached") //nolint:staticcheck // a product name; operator-visible text kept
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", errors.New("Tether refused the message (HTTP " + http.StatusText(resp.StatusCode) + ")") //nolint:staticcheck // a product name; operator-visible text kept
	}
	var out struct {
		ID        string `json:"id"`
		MessageID string `json:"message_id"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.ID == "" {
		out.ID = out.MessageID
	}
	return out.ID, nil
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
