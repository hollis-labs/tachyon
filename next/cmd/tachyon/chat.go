package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/appagent"
	"github.com/hollis-labs/tachyon/next/internal/assurance"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatactivity"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatapi"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatbridge"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatplan"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatthreads"
	"github.com/hollis-labs/tachyon/next/internal/chat/chattopics"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatwork"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatworkspace"
	"github.com/hollis-labs/tachyon/next/internal/dataverbs"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/planner"
	"github.com/hollis-labs/tachyon/next/internal/plannerassign"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/torqueread"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// chatWriteAction is the assurance action of every chat write and of the workspace file content
// read: an operator, as the old build's operator guard required (RequireOperator on writes,
// RequireOperatorReads on content).
const chatWriteAction assurance.Action = "chat.write"

// chatDeps are the chat services the routes serve: the threads over the inbound Tether chat bridge
// and the app agent (slice B6b), and the per-thread services (B6). Sending to an agent goes through the chat
// outbox (B6c); cards are a later slice.
type chatDeps struct {
	threads   *chatthreads.Handler
	turns     *chatbridge.Owned
	roster    http.Handler
	outbox    http.Handler
	send      http.Handler      // POST agents/{launch}/send (B6c)
	app       *appagent.Service // nil: the app agent is not configured
	topics    *chattopics.Handler
	plan      *chatplan.Service
	workspace *chatworkspace.Handler
	activity  *chatactivity.Handler
	work      http.Handler
	verbs     dataverbs.Chat // the agent data API's chat verbs (B6d)
	// What the quick terminal (B12) shares with the chat services, as the old server shared it.
	exists  func(ctx context.Context, ref string) error
	publish func(ctx context.Context, thread, typ string, fields map[string]any)
	rec     *chatactivity.Recorder
	ws      *chatworkspace.Store
}

// newChat builds the chat services as the old build's server did. Nothing here reaches an upstream
// unless it is configured: the Tether chat source needs tachyon.upstreams.tether.url (and its sync loop
// tachyon.features.chat_sync, started by startChat), and the app agent needs
// tachyon.upstreams.app_agent.url and .session.
func newChat(ctx context.Context, app *module.App, cfg settings.Settings, domain *store.Store) (chatDeps, error) {
	resolver := secrets.Resolver(app)
	// The Tether chat bridge. Its rows are keyed by the Tether address (SourceID), exactly as the old
	// build keyed them: tachyon.upstreams.tether.url must be spelled as the old TETHER_ADDR was.
	tetherURL := cfg.Upstreams.Tether.URL
	var source chatbridge.Source = unconfiguredTether{}
	sourceErr := errTetherOff
	if tetherURL != "" {
		if src, err := chatbridge.NewSource(tetherURL, upstreamauth.FromRef(resolver, cfg.Upstreams.Tether.Token)); err != nil {
			sourceErr = err
		} else {
			source, sourceErr = src, nil
		}
	}
	// The old build's default chat-turns cache policy (apicache): enabled, 3 s fresh, 60 s revalidate.
	turns := &chatbridge.Owned{Store: domain, Source: source, SourceID: tetherURL, ConfigError: sourceErr, Interval: cfg.ChatSyncInterval,
		FreshSeconds: 3, RevalidateSeconds: 60, CacheEnabled: true}
	var agent *appagent.Service
	if a := cfg.Upstreams.AppAgent; a.URL != "" {
		label := a.Label
		if label == "" {
			label = "Tachyon"
		}
		agent = &appagent.Service{Store: &appagent.Store{DB: domain},
			Runtime: appagent.NewNanite(appagent.NaniteConfig{URL: a.URL, Label: a.Label, Session: a.Session}, upstreamauth.FromRef(resolver, a.Token)), Label: label}
		if err := agent.Start(ctx); err != nil {
			return chatDeps{}, fmt.Errorf("app agent: %w", err)
		}
	}
	d := chatDeps{turns: turns, app: agent, roster: chatbridge.NewRoster(tetherURL, upstreamauth.FromRef(resolver, cfg.Upstreams.Tether.Token)),
		// The outbox rows are owned by the old build's digest of the Tether address, as the turns are keyed by it.
		outbox: chatbridge.OutboxList(domain, chatbridge.OutboxIdentity(tetherURL)),
		// Sending is outbound: off unless tachyon.features.chat_sync is on (plan section 6).
		send: chatbridge.NewSender(domain, tetherURL, upstreamauth.FromRef(resolver, cfg.Upstreams.Tether.Token), cfg.Features.ChatSync)}
	// The old server's startup backfill: link every migrated turn and outbox row of this Tether address
	// to its session object. Local writes only; nothing is fetched.
	if tetherURL != "" {
		if _, err := turns.BackfillSessionLinks(ctx, chatbridge.OutboxIdentity(tetherURL)); err != nil {
			return chatDeps{}, fmt.Errorf("chat session identity backfill: %w", err)
		}
	}
	d.threads = &chatthreads.Handler{
		Agg: &chatthreads.Aggregator{App: agent, Chat: turns, State: &chatthreads.StateStore{DB: domain}},
		Hub: &chatthreads.Hub{DB: domain},
	}
	exists := func(ctx context.Context, ref string) error { _, err := d.threads.Agg.Get(ctx, ref); return err }
	publish := func(ctx context.Context, thread, typ string, fields map[string]any) {
		_, _ = d.threads.Hub.Publish(ctx, thread, typ, fields)
	}
	// A Tether turn arriving or a reply changing delivery state is announced on the thread stream.
	turns.Changed = func(ctx context.Context, kind string, t chatbridge.Turn) {
		id := t.SessionID
		if id == "" {
			id = t.LocalSessionID
		}
		if id == "" {
			return
		}
		ref := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: id}.String()
		publish(context.WithoutCancel(ctx), ref, kind, map[string]any{"message_id": t.ID, "state": t.State, "delivery_state": t.Delivery, "kind": t.Kind})
		d.threads.Notify(ctx, ref)
	}
	rec := &chatactivity.Recorder{Store: &chatactivity.Store{DB: domain}, Publish: publish}
	ws := &chatworkspace.Store{DB: domain, Dir: cfg.WorkspaceDir}
	d.activity = &chatactivity.Handler{Rec: rec, Exists: exists}
	d.workspace = &chatworkspace.Handler{
		Store:  ws,
		Exists: exists,
		Published: func(ctx context.Context, thread, op, id string) {
			publish(ctx, thread, chatapi.EventWorkspace, map[string]any{"op": op, "id": id})
			if op == "share" {
				name := id
				if it, err := ws.Get(ctx, thread, id); err == nil {
					name = it.Name
				}
				rec.Quiet(ctx, thread, "file.shared", "user", "tachyon", []chatactivity.Cite{{Kind: "file", Ref: id, Label: name}}, chatactivity.Card("Shared "+name, "Shared with the agent", nil))
			}
		},
	}
	d.topics = &chattopics.Handler{
		Store:      &chattopics.Store{DB: domain},
		Exists:     exists,
		MessageSeq: d.threads.MessageSeq,
		Published: func(ctx context.Context, thread, typ, op, id string) {
			publish(ctx, thread, typ, map[string]any{"op": op, "id": id})
			if op == "add" {
				rec.Quiet(ctx, thread, typ+".added", "user", "tachyon", nil, chatactivity.Card(strings.ToUpper(typ[:1])+typ[1:]+" added", "", nil))
			}
		},
	}
	d.plan = &chatplan.Service{
		Store: &chatplan.Store{DB: domain, ContinueDefault: func(ctx context.Context) bool {
			st, err := ws.Settings(ctx)
			return err != nil || st.ContinueNewSessions
		}},
		Exists: exists,
		ProjectOf: func(ctx context.Context, ref string) (string, error) {
			st, err := d.threads.Agg.State.Get(ctx, ref)
			return st.ProjectID, err
		},
		Publish: publish,
		Note: func(ctx context.Context, thread string, p chatplan.Plan) {
			t, err := chatapi.ParseRef(thread)
			if err != nil || t.Source != chatapi.SourceApp || agent == nil {
				return
			}
			_, _ = agent.AppendNote(ctx, t.ID, "plan", planTitle(p), map[string]any{"plan_id": p.ID})
		},
		Notify: d.threads.Notify,
		Activity: func(ctx context.Context, thread, by, reason string, p chatplan.Plan) {
			actor := "user"
			if by == "agent" {
				actor = "agent"
			}
			pr := p.Progress()
			rec.Quiet(ctx, thread, "plan.changed", actor, "tachyon", nil, chatactivity.Card(planTitle(p)+": "+reason, fmt.Sprintf("%d of %d steps done", pr.Done, pr.Total), nil))
		},
		Carried: func(ctx context.Context, from, to string, p chatplan.Plan) {
			pr := p.Progress()
			rec.Quiet(ctx, to, "plan.carried", "tachyon", "tachyon", nil, chatactivity.Card(planTitle(p)+": carried over from "+from, fmt.Sprintf("%d of %d steps done", pr.Done, pr.Total), nil))
		},
	}
	d.threads.Agg.Sessions = func(ctx context.Context) []chatthreads.SessionStub {
		var out []chatthreads.SessionStub
		for _, st := range d.plan.Store.Sessions(ctx) {
			out = append(out, chatthreads.SessionStub{Ref: st.Ref, Agent: st.Agent, SeenNS: st.SeenNS, HasPlan: st.HasPlan})
		}
		return out
	}
	d.threads.Agg.PlanProgress = func(ctx context.Context, refs []string) map[string]chatthreads.Progress {
		out := map[string]chatthreads.Progress{}
		for ref, pr := range d.plan.Store.Progress(ctx, refs) {
			out[ref] = chatthreads.Progress{Done: pr.Done, Total: pr.Total}
		}
		return out
	}
	if agent != nil {
		app := func(id string) string { return chatapi.ThreadRef{Source: chatapi.SourceApp, ID: id}.String() }
		agent.OnTool = func(id string, t appagent.Tool) {
			if t.Status == "ok" || t.Status == "error" {
				rec.Quiet(context.Background(), app(id), "tool.call", "agent", "runtime", nil, chatactivity.Card(t.Name, t.Status, nil))
			}
		}
		agent.OnContext = func(id string, used, window int64, model string) {
			rec.SetContext(context.Background(), app(id), used, window, model)
		}
		agent.OnChange = func(id string) { d.threads.Notify(context.Background(), app(id)) }
	}
	// The Work view: the thread's plan links and the agent's Planner assignments, enriched from Torque
	// when it is configured (the old build's mountChatWork).
	torque := strings.TrimRight(strings.TrimSpace(cfg.Upstreams.Torque.URL), "/")
	token := upstreamauth.FromRef(secrets.Resolver(app), cfg.Upstreams.Torque.Token)
	assign := plannerassign.New(objects.New(domain), torque)
	work := &chatwork.Service{
		Plan: func(ctx context.Context, ref chatapi.ThreadRef) (chatplan.Plan, error) {
			return d.plan.Get(ctx, ref, "thread")
		},
		Assigned: assign.Work,
	}
	if torque != "" {
		assign.Tasks = planner.NewTasks(torque, token)
		work.Task = torqueread.New(torque, token).GetTask
	}
	d.exists, d.publish, d.rec, d.ws = exists, publish, rec, ws
	d.work = chatwork.Handler(work, exists)
	d.verbs = chatVerbServices(d, domain, publish, rec, exists)
	return d, nil
}

// chatVerbServices are the chat verbs' services, wired as the old server wired them: the same stores
// and thread check the routes use. A topic an agent adds is announced on the thread stream and
// recorded quietly as the agent's; plan ownership reads the thread's agent from the aggregator.
func chatVerbServices(d chatDeps, domain *store.Store, publish func(context.Context, string, string, map[string]any), rec *chatactivity.Recorder, exists func(context.Context, string) error) dataverbs.Chat {
	asThread := func(t chatthreads.Summary) dataverbs.ChatThread {
		return dataverbs.ChatThread{Ref: t.Ref, AgentID: t.Agent.AgentID, Address: t.Agent.Address, LastAt: t.LastAt, Archived: t.Archived}
	}
	agg := d.threads.Agg
	return dataverbs.Chat{
		Workspace: &dataverbs.ChatWorkspace{Store: d.workspace.Store, Thread: exists},
		Topics: &dataverbs.ChatTopics{Store: d.topics.Store, Thread: exists, MessageSeq: d.topics.MessageSeq, OpenApprovals: dataverbs.OpenApprovals(domain),
			Published: func(ctx context.Context, thread, op, id string) {
				publish(ctx, thread, chatapi.EventTopic, map[string]any{"op": op, "id": id})
				if op == "add" {
					rec.Quiet(ctx, thread, "topic.added", "agent", "tachyon", nil, chatactivity.Card("Topic added", "", nil))
				}
			}},
		Plan: &dataverbs.ChatPlan{Service: d.plan,
			Thread: func(ctx context.Context, ref string) (dataverbs.ChatThread, error) {
				t, err := agg.Get(ctx, ref)
				return asThread(t), err
			},
			Threads: func(ctx context.Context) ([]dataverbs.ChatThread, error) {
				ts, err := agg.Summaries(ctx)
				out := make([]dataverbs.ChatThread, len(ts))
				for i, t := range ts {
					out[i] = asThread(t)
				}
				return out, err
			}},
		Activity: &dataverbs.ChatActivity{Rec: rec, Thread: exists},
	}
}

// startChat runs the Tether chat sync loop when tachyon.features.chat_sync is on and Tether is
// configured. It returns a function that stops it and the app agent.
func startChat(cfg settings.Settings, d chatDeps) func() {
	syncing := cfg.Features.ChatSync && d.turns != nil && d.turns.ConfigError == nil
	if syncing {
		d.turns.Start()
	}
	return func() {
		if syncing {
			d.turns.Stop()
		}
		if d.app != nil {
			d.app.Stop()
		}
	}
}

// errTetherOff is the chat bridge's answer while tachyon.upstreams.tether.url is empty.
var errTetherOff = errors.New("Tether is not configured (tachyon.upstreams.tether.url)") //nolint:staticcheck // a product name

// unconfiguredTether is the chat source while Tether is not configured: every call is refused before
// any connection, so the bridge serves its local rows and never dials.
type unconfiguredTether struct{}

func (unconfiguredTether) Agents(context.Context) ([]chatbridge.Agent, error) {
	return nil, errTetherOff
}
func (unconfiguredTether) ChannelMessages(context.Context, string, tether.ChannelMessagesOptions) (tether.ChannelMessagesResponse, error) {
	return tether.ChannelMessagesResponse{}, errTetherOff
}
func (unconfiguredTether) RoutingCapabilities(context.Context, string) (tether.RoutingCapabilitiesResponse, error) {
	return tether.RoutingCapabilitiesResponse{}, errTetherOff
}
func (unconfiguredTether) Reply(context.Context, string, string, tether.ReplyOptions) (tether.ReplyReceipt, error) {
	return tether.ReplyReceipt{}, errTetherOff
}
func (unconfiguredTether) ReplyDelivery(context.Context, string) (tether.ReplyDelivery, error) {
	return tether.ReplyDelivery{}, errTetherOff
}

func planTitle(p chatplan.Plan) string {
	if p.Title != nil && *p.Title != "" {
		return *p.Title
	}
	return "Plan"
}

// chatRoutes are the old build's chat paths under chatapi.Prefix. Reads need tachyon.chat.read;
// writes, the workspace file content and GET .../continue (which records lineage) need
// tachyon.chat.write and the operator check (docs/api-contract.md section 5).
func chatRoutes(c chatDeps) []routes.Route {
	if c.plan == nil {
		return nil
	}
	write := func(h http.Handler) http.Handler { return assurance.Middleware(chatWriteAction, h) }
	var rs []routes.Route
	add := func(list []string, h http.Handler) {
		for _, r := range list {
			method, path, _ := strings.Cut(r, " ")
			scope, handler := "tachyon.chat.read", h
			if method != http.MethodGet || strings.HasSuffix(path, "/continue") {
				scope, handler = "tachyon.chat.write", write(h)
			}
			rs = append(rs, routes.Route{Method: method, Pattern: chatapi.Prefix + path, Scope: scope, Handler: handler})
		}
	}
	add(chatthreads.Routes, c.threads.Mux())
	add(chattopics.Routes, c.topics.Mux())
	add(chatplan.Routes, (&chatplan.Handler{Service: c.plan}).Mux())
	add(chatworkspace.Routes, c.workspace.Mux())
	add(chatactivity.Routes, c.activity.Mux())
	add(chatwork.Routes, c.work)
	method, path, _ := strings.Cut(chatworkspace.ContentRoute, " ")
	rs = append(rs, routes.Route{Method: method, Pattern: path, Scope: "tachyon.chat.write", Handler: write(c.workspace.Content())})
	// The Tether chat roster, passed through (the old console plugin's proxy).
	rs = append(rs, routes.Route{Method: http.MethodGet, Pattern: chatapi.Prefix + chatbridge.RosterPath, Scope: "tachyon.chat.read", Handler: c.roster},
		// The messages sent to a launch (the read side of the chat outbox).
		routes.Route{Method: http.MethodGet, Pattern: chatapi.Prefix + chatbridge.OutboxPath, Scope: "tachyon.chat.read", Handler: c.outbox},
		// Sending to an agent launch (B6c): a chat write, with the operator check.
		routes.Route{Method: http.MethodPost, Pattern: chatapi.Prefix + chatbridge.SendPath, Scope: "tachyon.chat.write", Handler: write(c.send)})
	// The Tether chat inbox (chat-turns) and the app agent, each under its own old prefix.
	rs = append(rs,
		routes.Route{Method: http.MethodGet, Pattern: chatbridge.Prefix + "/inbox", Scope: "tachyon.chat.read", Handler: c.turns},
		routes.Route{Method: http.MethodPost, Pattern: chatbridge.Prefix + "/items/{id}/reply", Scope: "tachyon.chat.write", Handler: write(c.turns)})
	agent := &appagent.Handler{Service: c.app, Disabled: "The app agent is not configured on this Tachyon (set tachyon.upstreams.app_agent)."}
	for _, r := range appagent.Routes {
		method, path, _ := strings.Cut(r, " ")
		scope, h := "tachyon.chat.read", http.Handler(agent)
		if method != http.MethodGet {
			scope, h = "tachyon.chat.write", write(agent)
		}
		rs = append(rs, routes.Route{Method: method, Pattern: appagent.Prefix + path, Scope: scope, Handler: h})
	}
	return rs
}
