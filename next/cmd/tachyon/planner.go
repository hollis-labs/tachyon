package main

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/assurance"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/planner"
	"github.com/hollis-labs/tachyon/next/internal/plannerassign"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// plannerWriteAction is the assurance action of every planner write: an operator (a signed-in
// person holding tachyon.operator), as the old build's operator guard required.
const plannerWriteAction assurance.Action = "planner.write"

// plannerDeps is the planner service the routes and the ingest worker share.
type plannerDeps struct {
	http   *planner.HTTP
	assign *plannerassign.Service
	ingest *planner.Ingest
}

func newPlanner(app *module.App, cfg settings.Settings, domain *store.Store) plannerDeps {
	torque := strings.TrimRight(strings.TrimSpace(cfg.Upstreams.Torque.URL), "/")
	tasks := planner.NewTasks(torque, upstreamauth.FromRef(secrets.Resolver(app), cfg.Upstreams.Torque.Token))
	objs := objects.New(domain)
	assign := plannerassign.New(objs, torque)
	planner.TaskAssignment = func(ctx context.Context, task string) planner.Item {
		as, err := assign.Assignees(ctx, task)
		if err != nil || len(as) == 0 {
			return nil
		}
		a := as[0]
		out := planner.Item{"id": a.ID, "state": a.State}
		if a.AgentID != "" {
			out["agent_id"] = a.AgentID
		} else {
			out["team_id"] = a.TeamID
		}
		return out
	}
	return plannerDeps{
		http:   planner.NewHTTP(domain, tasks),
		assign: assign,
		ingest: &planner.Ingest{Owned: &planner.Owned{Objects: objs}, Tasks: tasks, Events: planner.Events{DB: domain}, DB: domain},
	}
}

// plannerRoutes are the old build's planner paths. Reads need tachyon.planner.read; writes need
// tachyon.planner.write and the operator check. The more specific patterns are listed first: the
// auth table takes the first match, the mux the most specific.
func plannerRoutes(p plannerDeps) []routes.Route {
	if p.http == nil {
		return nil
	}
	write := func(h http.Handler) http.Handler { return assurance.Middleware(plannerWriteAction, h) }
	var rs []routes.Route
	for _, r := range plannerassign.Routes {
		method, path, _ := strings.Cut(r, " ")
		h := p.assign.Handler()
		scope := "tachyon.planner.read"
		if method != http.MethodGet {
			scope, h = "tachyon.planner.write", write(h)
		}
		rs = append(rs, routes.Route{Method: method, Pattern: planner.Prefix + path, Scope: scope, Handler: h})
	}
	for _, prefix := range []string{planner.Prefix, "/api/plugins/planner/v1"} {
		rs = append(rs,
			routes.Route{Method: http.MethodGet, Pattern: prefix + "/tasks/{task}", Scope: "tachyon.planner.read", Handler: http.HandlerFunc(p.http.TaskDetails)},
			routes.Route{Method: http.MethodGet, Pattern: prefix + "/tasks/{task}/comments", Scope: "tachyon.planner.read", Handler: http.HandlerFunc(p.http.TaskDetails)})
	}
	rs = append(rs, routes.Route{Method: http.MethodGet, Pattern: "/api/plugins/planner/stream/events", Scope: "tachyon.planner.read", Handler: http.HandlerFunc(p.http.Events)})
	for _, prefix := range []string{planner.Prefix, planner.LegacyPrefix} {
		rs = append(rs, routes.Route{Method: http.MethodGet, Pattern: prefix + "/", Scope: "tachyon.planner.read", Handler: p.http})
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			rs = append(rs, routes.Route{Method: m, Pattern: prefix + "/", Scope: "tachyon.planner.write", Handler: write(p.http)})
		}
	}
	return rs
}

// startPlannerIngest runs the planner ingest worker (Torque task moves and merged PRs become timeline
// events) when tachyon.features.planner_events is on. It returns a function that stops it and waits.
func startPlannerIngest(ctx context.Context, cfg settings.Settings, p plannerDeps) func() {
	if !cfg.Features.PlannerEvents || p.ingest == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { p.ingest.Run(ctx, 45*time.Second) })
	return func() { cancel(); wg.Wait() }
}
