package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/assurance"
	"github.com/hollis-labs/tachyon/next/internal/chat"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatactivity"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatapi"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatplan"
	"github.com/hollis-labs/tachyon/next/internal/chat/chattopics"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatwork"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatworkspace"
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

// chatDeps are the chat services the routes serve (slice B6: chat without threads). The threads,
// their messages and both event streams are slice B6b; until then each service's thread-existence
// check reads the local tables (chat.LocalThreads), and change events have no stream to go to.
type chatDeps struct {
	topics    *chattopics.Handler
	plan      *chatplan.Service
	workspace *chatworkspace.Handler
	activity  *chatactivity.Handler
	work      http.Handler
}

// newChat builds the chat services as the old build's server did, minus the threads hub.
func newChat(app *module.App, cfg settings.Settings, domain *store.Store) chatDeps {
	exists := chat.LocalThreads(domain)
	// No stream to publish to before B6b: the old build published every change to the threads hub.
	publish := func(context.Context, string, string, map[string]any) {}
	rec := &chatactivity.Recorder{Store: &chatactivity.Store{DB: domain}, Publish: publish}
	ws := &chatworkspace.Store{DB: domain, Dir: cfg.WorkspaceDir}
	d := chatDeps{
		activity: &chatactivity.Handler{Rec: rec, Exists: exists},
		workspace: &chatworkspace.Handler{
			Store:  ws,
			Exists: exists,
			Published: func(ctx context.Context, thread, op, id string) {
				if op == "share" {
					name := id
					if it, err := ws.Get(ctx, thread, id); err == nil {
						name = it.Name
					}
					rec.Quiet(ctx, thread, "file.shared", "user", "tachyon", []chatactivity.Cite{{Kind: "file", Ref: id, Label: name}}, chatactivity.Card("Shared "+name, "Shared with the agent", nil))
				}
			},
		},
		topics: &chattopics.Handler{
			Store:  &chattopics.Store{DB: domain},
			Exists: exists,
			// MessageSeq resolves a message in its thread from the thread's messages, which are B6b's.
			// Until then a topic's message_ref is kept as given and its seq is null.
			Published: func(ctx context.Context, thread, typ, op, _ string) {
				if op == "add" {
					rec.Quiet(ctx, thread, typ+".added", "user", "tachyon", nil, chatactivity.Card(strings.ToUpper(typ[:1])+typ[1:]+" added", "", nil))
				}
			},
		},
	}
	d.plan = &chatplan.Service{
		Store: &chatplan.Store{DB: domain, ContinueDefault: func(ctx context.Context) bool {
			st, err := ws.Settings(ctx)
			return err != nil || st.ContinueNewSessions
		}},
		Exists:  exists,
		Publish: publish,
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
	d.work = chatwork.Handler(work, exists)
	return d
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
	add(chattopics.Routes, c.topics.Mux())
	add(chatplan.Routes, (&chatplan.Handler{Service: c.plan}).Mux())
	add(chatworkspace.Routes, c.workspace.Mux())
	add(chatactivity.Routes, c.activity.Mux())
	add(chatwork.Routes, c.work)
	method, path, _ := strings.Cut(chatworkspace.ContentRoute, " ")
	rs = append(rs, routes.Route{Method: method, Pattern: path, Scope: "tachyon.chat.write", Handler: write(c.workspace.Content())})
	return rs
}
