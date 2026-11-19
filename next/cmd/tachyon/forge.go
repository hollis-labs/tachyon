package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/assurance"
	"github.com/hollis-labs/tachyon/next/internal/forgeapi"
	"github.com/hollis-labs/tachyon/next/internal/forgeaudit"
	"github.com/hollis-labs/tachyon/next/internal/forgeindex"
	"github.com/hollis-labs/tachyon/next/internal/forgereads"
	"github.com/hollis-labs/tachyon/next/internal/itemstate"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// The forge (pull requests) domain, read side (slice B11a): the pending inbox, status and event
// stream from the PR index; the PR workspace reads (viewer, PR detail, files, diff, checks, commits,
// comments, a commit by sha) from GitHub; the forge audit read; the console item state (the inbox
// triage) list and PUT.
//
// tachyon.features.forge_index (default off) gates every outbound GitHub request: the poller, and each
// on-demand read. Off, nothing dials GitHub: the reads that need GitHub answer 503 disabled, and the
// reads served from the store (pending from the last saved index, status, stream, audit, console state)
// keep working.

const (
	forgeRead  = "tachyon.forge.read"
	forgeWrite = "tachyon.forge.write"
	// forgeAuditAction is the operator check on the audit read (the old RequireOperatorReads). It is
	// not in the assurance table, so it needs operator-token: an unknown action fails closed.
	forgeAuditAction assurance.Action = "forge.audit_read"
	// forgeStateWriteAction is the operator check on PUT console/state/{key} (the old RequireOperator on
	// writes), likewise outside the assurance table and so operator-token.
	forgeStateWriteAction assurance.Action = "forge.state_write"
	// forgeDisabled is the message of every GitHub read while the switch is off.
	forgeDisabled = "GitHub reads are off (tachyon.features.forge_index); nothing was sent"
	// githubDefaultURL is the API base when tachyon.upstreams.github.url is empty (the old default).
	githubDefaultURL = "https://api.github.com"
	// triagePrefix is the item-state key prefix of the PR inbox's triage rows.
	triagePrefix = "pr-inbox:"
)

type forgeDeps struct {
	index   *forgeindex.Index
	pending http.Handler // GET pulls/pending, status, stream
	reads   http.Handler // the GitHub reads
	audit   http.Handler
	state   *itemstate.Store
	cookie  string // the identity module's session cookie (the bearer parity of PUT console/state)
	// poll runs the poller; nil when it does not run (switch off, or the client cannot be built).
	poll func(ctx context.Context)
}

// newForge builds the forge read side over the domain store without starting anything.
func newForge(app *module.App, cfg settings.Settings, domain *store.Store) forgeDeps {
	gh := cfg.Upstreams.GitHub
	base := strings.TrimRight(strings.TrimSpace(gh.URL), "/")
	if base == "" {
		base = githubDefaultURL
	}
	token := upstreamauth.FromRef(secrets.Resolver(app), gh.Token)
	client := &forgeindex.GitHub{Base: base}
	reads := &forgereads.Handler{Base: base, Disabled: forgeDisabled, Org: gh.Org}
	x := &forgeindex.Index{DB: domain, GH: client, Bus: forgeindex.NewBroker(nil),
		Settings: forgeindex.AppSettings{DB: domain, Org: gh.Org},
		Disabled: "GitHub reads are off (tachyon.features.forge_index); showing the last saved data."}
	d := forgeDeps{index: x, audit: (&forgeaudit.Log{DB: domain}).Handler(), state: &itemstate.Store{DB: domain}, cookie: app.Name + "_session"}
	if cfg.Features.ForgeIndex {
		hc, err := githubClient(base, token)
		if err != nil {
			// The error names the problem (a token over plain http to a remote host), never a value.
			slog.Warn("forge: the GitHub client cannot be built; GitHub reads stay off", "err", err)
			reads.Disabled = "The GitHub upstream is not usable: " + err.Error()
			x.Disabled = reads.Disabled
		} else {
			client.HTTP, client.Credential = hc, token != nil
			reads.HTTP, reads.Credential, reads.Disabled = hc, token != nil, ""
			x.Disabled = ""
			d.poll = x.Run
		}
	}
	d.reads = reads.Mux()
	api := &forgeindex.API{Index: x, Triage: triage(d.state), Agents: linkedAgents(domain)}
	d.pending = api.Handler()
	return d
}

// start runs the item-state snooze wake loop (local tables only, as the old server always did) and,
// when the switch is on, the PR index poller. It returns a function that stops them.
func (d forgeDeps) start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); d.state.Maintain(ctx, itemstate.WakeInterval, slog.Default().Warn) }()
	if d.poll != nil {
		wg.Add(1)
		go func() { defer wg.Done(); d.poll(ctx) }()
	}
	return func() { cancel(); wg.Wait() }
}

// githubClient is the GitHub API client: the credential for base (refused over http to a non-loopback
// host), a User-Agent (GitHub requires one), 15 s for the headers and 20 s overall, and redirects only
// within base's origin (a renamed repository answers one).
func githubClient(base string, token upstreamauth.Token) (*http.Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("tachyon.upstreams.github.url is not an http(s) URL")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 15 * time.Second
	rt, err := upstreamauth.Wrap(upstreamauth.WithHeader(tr, "User-Agent", "tachyon"), base, token)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != u.Scheme || req.URL.Host != u.Host || len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	}}, nil
}

// triage reads the inbox triage rows (item state keyed pr-inbox:<pr key>) as the pending inbox's
// states, mapped as the old server did.
func triage(st *itemstate.Store) func(ctx context.Context) (map[string]forgeindex.Triage, error) {
	return func(ctx context.Context) (map[string]forgeindex.Triage, error) {
		rows, err := st.List(ctx, triagePrefix+"github:")
		if err != nil {
			return nil, err
		}
		out := map[string]forgeindex.Triage{}
		for _, r := range rows {
			t := forgeindex.Triage{Status: "active", By: r.By}
			switch r.Status {
			case "snoozed":
				t.Status = "snoozed"
				if r.SnoozeUtil != nil {
					t.SnoozeUntilMS = r.SnoozeUtil.UnixMilli()
				}
			case "dismissed":
				t.Status = "dismissed"
			case "settled":
				t.Status = "resolved"
			}
			out[strings.TrimPrefix(r.Key, triagePrefix)] = t
		}
		return out, nil
	}
}

// linkedAgents reads the open agent assignments (pr_agent_links) as kind "agent" assignees by PR key:
// the read the old server made through forgeassign.Handler.Assignees.
func linkedAgents(db *store.Store) func(ctx context.Context) (map[string][]forgeindex.Assignee, error) {
	return func(ctx context.Context) (map[string][]forgeindex.Assignee, error) {
		rows, err := db.Reader.QueryContext(ctx, `SELECT pr_key, agent_id, agent_urn, task, status FROM pr_agent_links WHERE status <> 'cancelled' AND status <> 'done' ORDER BY created_ms`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		out := map[string][]forgeindex.Assignee{}
		for rows.Next() {
			var key string
			var a forgeindex.Assignee
			if err := rows.Scan(&key, &a.ID, &a.URN, &a.Task, &a.Status); err != nil {
				return nil, err
			}
			a.Kind, a.Label = "agent", a.ID
			out[key] = append(out[key], a)
		}
		return out, rows.Err()
	}
}

// forgeRoutes are the forge read routes and the console state list and PUT (docs/api-contract.md, the
// forge row): reads under tachyon.forge.read, the PUT under tachyon.forge.write.
//
// The old guards, route by route:
//   - pulls/pending, status, stream (forgeindex), viewer, files, diff, checks, commits/{sha}
//     (forgereads): RequireOperator, which let every GET through. Here: the route scope only.
//   - pulls/{num}, …/commits, …/comments, repos/{…}, repos/{…}/pulls, forge/pulls: the console plugin
//     behind the unguarded /api/plugins/ catch-all. Here: the route scope only.
//   - audit: RequireOperatorReads, operator assurance on the read. Here: the route scope plus the
//     operator check (assurance; an API key never qualifies).
//   - GET console/state: RequireOperator, which let GETs through. Here: the route scope only.
//   - PUT console/state/{key}: RequireOperator (operator assurance on writes) and the handler refused
//     any Authorization header (403 requester_cannot_approve). Here: the write scope, the same
//     refusal of a bearer that is not the browser session's (stateGuard), then the operator check
//     (assurance, forgeStateWriteAction), for parity with the old guard. The contract
//     (docs/api-contract.md section 5) lists no operator check on console state; the port follows the
//     old build pending the lead's ruling.
//
// None of these routes refused a bearer except the console state PUT, so only it keeps that refusal.
func forgeRoutes(d forgeDeps) []routes.Route {
	if d.pending == nil {
		return nil
	}
	rs := []routes.Route{}
	for _, pat := range forgeindex.Routes() {
		rs = append(rs, routes.Route{Method: http.MethodGet, Pattern: strings.TrimPrefix(pat, "GET "), Scope: forgeRead, Handler: d.pending})
	}
	for _, pat := range forgereads.Routes {
		rs = append(rs, routes.Route{Method: http.MethodGet, Pattern: strings.TrimPrefix(pat, "GET "), Scope: forgeRead, Handler: d.reads})
	}
	state := d.state.Handler(forgeapi.StatePrefix)
	return append(rs,
		routes.Route{Method: http.MethodGet, Pattern: forgeapi.Prefix + "/audit", Scope: forgeRead, Handler: assurance.Middleware(forgeAuditAction, d.audit)},
		routes.Route{Method: http.MethodGet, Pattern: forgeapi.StatePrefix, Scope: forgeRead, Handler: state},
		// A subtree: a key is URL-encoded by the client, and a PR key's "/" arrives as a path
		// separator in the decoded path the guard's scope table matches (one {key} segment would not).
		routes.Route{Method: http.MethodPut, Pattern: forgeapi.StatePrefix + "/", Scope: forgeWrite, Handler: stateGuard(d.cookie, assurance.Middleware(forgeStateWriteAction, state))},
	)
}

// stateGuard keeps console state writes for the browser session, after the route's scope check.
//
// The old handler refused any request carrying an Authorization header (403 requester_cannot_approve,
// "Item state is changed from the operator's browser session only."). The identity middleware now
// turns the session cookie into "Authorization: Bearer <cookie value>" before the guard sees the
// request, so, as for transcripts (slice B10), a bearer is the browser session's only when it is
// exactly the session cookie; any other bearer (an API key, a minted token) is refused as before.
func stateGuard(cookie string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Authorization"); h != "" && !fromSessionCookie(r, cookie, h) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "requester_cannot_approve", "message": "Item state is changed from the operator's browser session only."})
			return
		}
		next.ServeHTTP(w, r)
	})
}
