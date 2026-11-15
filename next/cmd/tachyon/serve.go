package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"
	"github.com/hollis-labs/substrate/modules/scheduler"

	"github.com/hollis-labs/tachyon/next/internal/access"
	"github.com/hollis-labs/tachyon/next/internal/approvals"
	"github.com/hollis-labs/tachyon/next/internal/dataverbs"
	"github.com/hollis-labs/tachyon/next/internal/hostguard"
	"github.com/hollis-labs/tachyon/next/internal/objects"
	"github.com/hollis-labs/tachyon/next/internal/objectsync"
	"github.com/hollis-labs/tachyon/next/internal/pluginallow"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/webui"
)

// tachyon is what the commands share: the started framework app. ready, when set, learns the bound
// address (tests listen on port 0).
type tachyon struct {
	app   *module.App
	ready func(net.Addr)
}

// deps are what the routes serve.
type deps struct {
	access    *access.Service
	local     *dataverbs.Local
	objectJob *objectsync.Jobs
	planner   plannerDeps
	chat      chatDeps
	approvals *approvals.Inbox
	agents    agentsDeps
	builder   builderDeps
	terminal  terminalDeps
}

// appRoutes are Tachyon's own routes, each with its scope (internal/routes). The domain slices add
// theirs here. The agent data API answers only API keys mapped to a service identity; its route
// scope is "authenticated" and each verb is checked in the handler against the key's verb scope and
// the credential's kinds. The console data API answers signed-in people, each verb behind its scope.
func appRoutes(d deps) []routes.Route {
	agent := dataverbs.NewAgentHTTP(d.local, d.access, d.chat.verbs)
	console := dataverbs.NewConsoleHTTP(d.local, d.builder.teams)
	return append(append(append(append(append(plannerRoutes(d.planner), chatRoutes(d.chat)...), approvalsRoutes(d.approvals)...), agentsRoutes(d.agents)...), builderRoutes(d.builder)...), append(terminalRoutes(d.terminal), []routes.Route{
		{Method: http.MethodGet, Pattern: dataverbs.AgentPrefix + "/providers", Scope: routes.Authenticated, Handler: agent},
		{Method: http.MethodGet, Pattern: dataverbs.AgentPrefix + "/providers/{provider}/verbs/{verb}", Scope: routes.Authenticated, Handler: agent},
		{Method: http.MethodPost, Pattern: dataverbs.AgentPrefix + "/providers/{provider}/verbs/{verb}", Scope: routes.Authenticated, Handler: agent},
		{Method: http.MethodGet, Pattern: dataverbs.Prefix + "/providers", Scope: routes.Authenticated, Handler: console},
		{Method: http.MethodGet, Pattern: dataverbs.Prefix + "/providers/{provider}/verbs/{verb}", Scope: routes.Authenticated, Handler: console},
		{Method: http.MethodPost, Pattern: dataverbs.Prefix + "/providers/{provider}/verbs/{verb}", Scope: routes.Authenticated, Handler: console},
		{Method: http.MethodGet, Pattern: objectSyncStatusPath, Scope: "tachyon.objects.read", Handler: objectSyncStatus(d.objectJob)},
	}...)...)
}

// pluginAllowlist are the (method, path) pairs under /api/plugins/ that reach the plugin host
// (docs/api-contract.md). Empty until the plugin slices add theirs: nothing is forwarded.
var pluginAllowlist []pluginallow.Entry

func (t *tachyon) serveCmd(ctx context.Context) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the Tachyon UI and API",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return t.serve(ctx) },
	}
}

func (t *tachyon) serve(ctx context.Context) error {
	app := t.app
	cfg, err := settings.Parse(app.Config[settings.Key])
	if err != nil {
		return err
	}
	if err = requireLoopback(app); err != nil {
		return err
	}
	id := identity.From(app)
	table := auth.ScopeTable(app)
	if id == nil || table == nil {
		return errors.New("the identity and auth modules must be started")
	}
	db := store.From(app)
	jobs, _, err := objectSync(app, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = jobs.Close() }()
	// The runner is registered whether or not object sync is on: it is the engine that the feature
	// switch gates (scheduler.Run below), and with the engine stopped nothing is ever dispatched.
	setRunner(objectsync.JobType, jobs)
	defer setRunner(objectsync.JobType, nil)
	domain := store.Wrap(db)
	pl := newPlanner(app, cfg, domain)
	stopIngest := startPlannerIngest(ctx, cfg, pl)
	defer stopIngest()
	chat, err := newChat(ctx, app, cfg, domain)
	if err != nil {
		return err
	}
	stopChat := startChat(cfg, chat)
	defer stopChat()
	inbox := newApprovals(ctx, app, cfg, domain)
	stopApprovals := startApprovals(inbox)
	defer stopApprovals()
	agents := newAgents(app, cfg, domain, chat)
	stopAgents := agents.start(ctx)
	defer stopAgents()
	builder := newBuilder(app, cfg, domain)
	stopBuilder := builder.start(ctx)
	defer stopBuilder()
	term, err := newTerminal(ctx, app, cfg, domain, chat)
	if err != nil {
		return err
	}
	chat.verbs.Terminal = terminalVerbs(term)
	stopTerminal := term.start(ctx)
	defer stopTerminal()
	appRts := appRoutes(deps{access: access.New(db, id, nil), local: dataverbs.NewLocal(objects.New(domain)), objectJob: jobs, planner: pl, chat: chat, approvals: inbox, agents: agents, builder: builder, terminal: term})
	mux, err := routes.Register(table, appRts)
	if err != nil {
		return err
	}
	assets, err := webui.Assets()
	if err != nil {
		return err
	}
	handler, err := httphost.Handler(app, httphost.Options{Assets: assets, Routes: mux})
	if err != nil {
		return err
	}
	// Outermost first: the Host check, then the session cookie becomes the bearer the guard
	// verifies, then the plugin allowlist, then the host (guard, routes, SPA).
	// Tachyon's own routes under /api/plugins/ (the console paths) pass the plugin filter; everything
	// else there must be on the plugin allowlist.
	allow := slices.Clone(pluginAllowlist)
	for _, r := range appRts {
		allow = append(allow, pluginallow.Entry{Method: r.Method, Pattern: r.Pattern})
	}
	handler = hostguard.Handler(cfg.AllowedHosts, id.Middleware(pluginallow.Handler(allow, handler)))

	// Outbound work starts only when its switch is on (port plan section 6).
	if cfg.Features.ObjectSync {
		if err = scheduler.Run(app); err != nil {
			return err
		}
	}

	return httphost.Serve(ctx, app, handler, httphost.ServeOptions{
		Ready: func(addr net.Addr) {
			slog.Info("tachyon listening", "addr", addr.String())
			if t.ready != nil {
				t.ready(addr)
			}
		},
	})
}

// requireLoopback keeps the backend on the loopback interface: the Host check assumes it.
func requireLoopback(app *module.App) error {
	cfg, _ := app.Config["httphost"].(map[string]any)
	addr, _ := cfg["addr"].(string)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("tachyon requires a loopback httphost.addr (for example 127.0.0.1:8093), got %q", addr)
}
