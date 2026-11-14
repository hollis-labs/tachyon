package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"
	"github.com/hollis-labs/substrate/modules/scheduler"

	"github.com/hollis-labs/tachyon/next/internal/access"
	"github.com/hollis-labs/tachyon/next/internal/hostguard"
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

// appRoutes are Tachyon's own routes, each with its scope (internal/routes). The domain slices add
// theirs here. The agent data API answers only API keys mapped to a service identity
// (access.RequireAgent); its route scope is "authenticated" and each verb is checked in the
// handler against the key's verb scope and the credential's kinds.
func appRoutes(acc *access.Service) []routes.Route {
	return []routes.Route{
		{Method: http.MethodGet, Pattern: access.DiscoveryPattern, Scope: routes.Authenticated, Handler: access.RequireAgent(acc, access.Discovery())},
	}
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
	acc := access.New(store.From(app), id, nil)
	mux, err := routes.Register(table, appRoutes(acc))
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
	handler = hostguard.Handler(cfg.AllowedHosts, id.Middleware(pluginallow.Handler(pluginAllowlist, handler)))

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
