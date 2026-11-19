package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/hollis-labs/substrate/module"

	"github.com/hollis-labs/tachyon/next/internal/assurance"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatactivity"
	"github.com/hollis-labs/tachyon/next/internal/chat/chatapi"
	"github.com/hollis-labs/tachyon/next/internal/dataverbs"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/terminal"
)

// The quick terminal (slice B12): an interactive ssh pane in a pty over a WebSocket, and approval-gated
// agent commands run over ssh, from the migrated terminal_sessions, terminal_runs and terminal_proposals.
// Both start ssh, so the terminal runs only with tachyon.features.terminal; off, it is the old build with
// no terminal service: the routes are not mounted (404) and the agent verbs are not listed.
//
// Every route needs tachyon.terminal.control and the operator check, reads and the WebSocket included (the
// old RequireOperatorReads); a bearer that is not the browser session's own cookie is refused, and the
// WebSocket upgrade is same-origin only (internal/terminal).

// terminalAction is the assurance action of every terminal route.
const terminalAction assurance.Action = "terminal.control"

const terminalScope = "tachyon.terminal.control"

type terminalDeps struct {
	svc     *terminal.Service // nil: the terminal is off
	handler http.Handler
	start   func(ctx context.Context) (stop func())
}

// newTerminal builds the terminal as the old server did. It closes the sessions a restart left open and
// fails the runs it left running, whether or not the terminal is on: there is no process to reattach.
func newTerminal(ctx context.Context, app *module.App, cfg settings.Settings, domain *store.Store, chat chatDeps) (terminalDeps, error) {
	st := &terminal.Store{DB: domain}
	if err := st.CloseStaleSessions(ctx); err != nil {
		return terminalDeps{}, fmt.Errorf("terminal startup sweep: %w", err)
	}
	d := terminalDeps{start: func(context.Context) func() { return func() {} }}
	if !cfg.Features.Terminal {
		return d, nil
	}
	// Machines come from the policy file (tachyon.files.terminal_policy; empty: none, and every agent
	// command asks) and the host user's ~/.ssh/config aliases; the client only ever names a machine id.
	tcfg := &terminal.Config{PolicyFile: cfg.Files.TerminalPolicy, SSHConfig: terminal.DefaultSSHConfig()}
	base, cancel := context.WithCancel(context.WithoutCancel(ctx))
	mgr := &terminal.Manager{Store: st, Config: tcfg, Base: base, Publish: chat.publish,
		Ended: func(ctx context.Context, r terminal.Run) {
			if chat.rec == nil {
				return
			}
			title := "Ran " + r.Command
			summary := r.State
			if r.ExitCode != nil {
				summary = fmt.Sprintf("%s, exit %d", r.State, *r.ExitCode)
			}
			chat.rec.Quiet(ctx, r.ThreadRef, "terminal.run", r.RequestedBy, "tachyon", nil, chatactivity.Card(title, summary, nil))
		}}
	agent := chat.app
	d.svc = &terminal.Service{Store: st, Mgr: mgr, Config: tcfg, Publish: chat.publish, Exists: chat.exists,
		CloseOnComplete: func(ctx context.Context) bool {
			if chat.ws == nil {
				return false
			}
			s, err := chat.ws.Settings(ctx)
			return err == nil && s.TerminalCloseOnComplete
		},
		Message: func(ctx context.Context, thread, runID string) (string, error) {
			t, perr := chatapi.ParseRef(thread)
			if perr != nil || t.Source != chatapi.SourceApp || agent == nil {
				return "", nil //nolint:nilerr // as before: a Tether thread (or none) has no stream Tachyon writes to
			}
			m, err := agent.AppendNote(ctx, t.ID, "run", "", map[string]any{"run_id": runID})
			return m.ID, err
		}}
	d.handler = (&terminal.Handler{Svc: d.svc, Cookie: app.Name + "_session"}).Mux()
	d.start = func(context.Context) func() {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); d.svc.Sweep(base) }()
		return func() {
			// Canceling the base kills every pty process group; Wait returns once each is reaped.
			cancel()
			wg.Wait()
			mgr.Wait()
		}
	}
	slog.Info("terminal is on", "policy_file", cfg.Files.TerminalPolicy != "")
	return d, nil
}

// terminalRoutes are the old terminal routes, each with its scope and the operator check; none while the
// terminal is off.
func terminalRoutes(d terminalDeps) []routes.Route {
	if d.handler == nil {
		return nil
	}
	h := assurance.Middleware(terminalAction, d.handler)
	var out []routes.Route
	for _, r := range terminal.Routes {
		method, path, _ := strings.Cut(r, " ")
		out = append(out, routes.Route{Method: method, Pattern: chatapi.TerminalPrefix + path, Scope: terminalScope, Handler: h})
	}
	return out
}

// terminalVerbs are the agent verbs' service: nil while the terminal is off.
func terminalVerbs(d terminalDeps) *dataverbs.ChatTerminal {
	if d.svc == nil {
		return nil
	}
	return &dataverbs.ChatTerminal{Service: d.svc}
}
