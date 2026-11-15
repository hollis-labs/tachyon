package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	libsecrets "github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/assurance"
	"github.com/hollis-labs/tachyon/next/internal/builderapi"
	"github.com/hollis-labs/tachyon/next/internal/builderchange"
	"github.com/hollis-labs/tachyon/next/internal/builderread"
	"github.com/hollis-labs/tachyon/next/internal/catalogindex"
	"github.com/hollis-labs/tachyon/next/internal/dataverbs"
	"github.com/hollis-labs/tachyon/next/internal/provideractions"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/tetherpass"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// The builder and catalog domain. Reads (slice B9a): the catalog index's summary and stream, the
// Builder's agents, launches and library reads, the registry's projects, the team policy and the imported
// team snapshot, from the migrated tables; the catalog index loop runs only with
// tachyon.features.catalog_index. Writes (slice B9b): builder changes, the launch action and the registry
// writes, which reach Tether, run only with tachyon.features.builder_changes. Team operations (slice B9b-2):
// the Tether provider's data verbs, served on the console data API; the ones that reach Tether bind to it
// only with the same switch, and off they answer unavailable as an old build without Tether did.

// builderWriteAction is the assurance action of every Builder and registry write: an operator, as the old
// RequireOperator required for writes.
const builderWriteAction assurance.Action = "builder.write"

// The catalog reads the Builder uses (the old console plugin and API cache paths).
const (
	teamPolicyPath    = "/api/plugins/console/v1/catalog/team-policy"
	importedTeamsPath = "/api/plugins/console/v1/catalog/imported-teams"
)

type builderDeps struct {
	index   *catalogindex.Index
	reader  *builderread.Reader
	tether  *tetherpass.Proxy
	db      *store.Store
	changes *builderchange.Service // nil: builder changes are off (503 changes_unavailable)
	teams   *dataverbs.Actions     // the Tether provider (team operations) on the console data API
	start   func(ctx context.Context) (stop func())
}

// newBuilder builds the read side without starting anything. The live session counts and the registry
// read a configured Tether on demand, as before.
func newBuilder(app *module.App, cfg settings.Settings, domain *store.Store) builderDeps {
	resolver := secrets.Resolver(app)
	token := upstreamauth.FromRef(resolver, cfg.Upstreams.Tether.Token)
	idx := &catalogindex.Index{Store: &catalogindex.Store{DB: domain}, Bus: catalogindex.NewBroker(time.Now)}
	d := builderDeps{index: idx, reader: &builderread.Reader{Index: idx.Store}, tether: tetherpass.New(cfg.Upstreams.Tether.URL, token), db: domain}
	d.teams = &dataverbs.Actions{Service: &provideractions.Service{Store: domain, Bind: teamsBind(cfg, token)}}
	if cfg.Upstreams.Tether.URL != "" {
		if sess, err := builderread.NewTetherSessions(cfg.Upstreams.Tether.URL, token); err == nil {
			d.reader.Sessions = sess
		}
		if projects, err := builderread.NewTetherProjects(cfg.Upstreams.Tether.URL, token); err == nil {
			d.reader.Projects = projects
		}
	}
	if cfg.Features.BuilderChanges {
		d.changes = newBuilderChanges(cfg, resolver, idx)
		// Registry writes use the control credential (registry.write); without it they answer 501, as before.
		if ref := cfg.Upstreams.TetherControl.Token; ref != "" {
			if base, client, err := builderchange.NewControlClient(controlAddress(cfg), upstreamauth.FromRef(resolver, ref)); err == nil {
				d.reader.Writer = builderread.NewProjectWriter(base, client)
			}
		}
	}
	d.start = func(ctx context.Context) func() {
		ctx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		if d.changes != nil {
			wg.Add(1)
			go func() { defer wg.Done(); d.changes.Run(ctx) }()
		}
		indexing := false
		if cfg.Features.CatalogIndex && strings.TrimSpace(cfg.Upstreams.Tether.URL) != "" {
			if src, err := catalogindex.NewHTTPSource(cfg.Upstreams.Tether.URL, token); err != nil {
				slog.Warn("catalog index is enabled but not configured; staying off", "err", err)
			} else {
				idx.Source = src
				idx.Start(ctx)
				indexing = true
			}
		}
		return func() {
			cancel()
			if indexing {
				idx.Wait(context.Background())
			}
			wg.Wait()
		}
	}
	return d
}

// errTeamsOff is the binding error with builder changes off; the service answers it as unavailable.
var errTeamsOff = errors.New("team operations are off (tachyon.features.builder_changes)")

// teamsBind binds the Tether provider to Tether on the read credential, as the old server did, only with
// tachyon.features.builder_changes on. Off, every verb that would reach Tether fails before dialing.
func teamsBind(cfg settings.Settings, token upstreamauth.Token) func() (provideractions.Bound, error) {
	if !cfg.Features.BuilderChanges {
		return func() (provideractions.Bound, error) { return nil, errTeamsOff }
	}
	return provideractions.Tether(cfg.Upstreams.Tether.URL, token)
}

// controlAddress is where the control credential reaches Tether: tachyon.upstreams.tether_control.url (the
// old TACHYON_TETHER_CONTROL_ADDR, Tether's local listener for plan writes), else the Tether address.
func controlAddress(cfg settings.Settings) string {
	if a := strings.TrimSpace(cfg.Upstreams.TetherControl.URL); a != "" {
		return a
	}
	return cfg.Upstreams.Tether.URL
}

// newBuilderChanges is the change service as the old server built it. With the control credential it plans
// and applies itself (direct); without one it runs in handoff mode on the read credential, ending at a
// bundle the operator plans in Tether. With neither it is off.
func newBuilderChanges(cfg settings.Settings, resolver *libsecrets.Resolver, idx *catalogindex.Index) *builderchange.Service {
	mode, ref, addr := builderapi.ModeDirect, cfg.Upstreams.TetherControl.Token, controlAddress(cfg)
	if ref == "" {
		mode, ref, addr = builderapi.ModeHandoff, cfg.Upstreams.Tether.Token, cfg.Upstreams.Tether.URL
	}
	if ref == "" {
		slog.Info("builder changes are off: no Tether credential is set")
		return nil
	}
	t, err := builderchange.NewTether(addr, upstreamauth.FromRef(resolver, ref))
	if err != nil {
		slog.Warn("builder changes are off", "err", err)
		return nil
	}
	if mode == builderapi.ModeHandoff {
		t = builderchange.ReadOnly(t)
	}
	return &builderchange.Service{Mode: mode, DB: idx.Store.DB, Tether: t, Index: idx.Store, Bus: idx.Bus}
}

// builderReadRoutes are the Builder and registry reads builderread serves (relative to their prefixes).
var builderReadRoutes = []string{
	"/agents", "/agents/{id}", "/agents/{id}/used_by", "/agents/{id}/checks", "/agents/{id}/history", "/agents/{id}/resolved",
	"/launches", "/launches/{id}", "/library/{kind}", "/library/{kind}/{id}", "/library/{kind}/{id}/used_by", "/library/{kind}/{id}/history",
}

// builderRoutes are the read routes: the Builder and registry under tachyon.builder.read, the catalog
// reads under tachyon.catalog.read (docs/api-contract.md, 5f).
func builderRoutes(d builderDeps) []routes.Route {
	if d.reader == nil {
		return nil
	}
	mux := http.NewServeMux()
	d.reader.Mount(mux, func(h http.Handler) http.Handler { return h })
	const read, catalog = "tachyon.builder.read", "tachyon.catalog.read"
	rs := []routes.Route{
		{Method: http.MethodGet, Pattern: builderapi.Prefix + "/summary", Scope: read, Handler: d.index.SummaryHandler()},
		{Method: http.MethodGet, Pattern: builderapi.Prefix + "/stream", Scope: read, Handler: d.index.StreamHandler()},
		{Method: http.MethodGet, Pattern: builderapi.RegistryPrefix + "/projects", Scope: read, Handler: mux},
		{Method: http.MethodGet, Pattern: builderapi.RegistryPrefix + "/projects/{id}", Scope: read, Handler: mux},
		// The one credential-free configuration document the old proxy read, not arbitrary file access.
		{Method: http.MethodGet, Pattern: teamPolicyPath, Scope: catalog, Handler: d.tether.Handler(tetherpass.Op{Target: func(*http.Request) (string, url.Values, error) {
			return "/catalog/files", url.Values{"path": {"teams-policy.yaml"}}, nil
		}})},
		{Method: http.MethodGet, Pattern: importedTeamsPath, Scope: catalog, Handler: importedTeams(d.db)},
	}
	for _, p := range builderReadRoutes {
		rs = append(rs, routes.Route{Method: http.MethodGet, Pattern: builderapi.Prefix + p, Scope: read, Handler: mux})
	}
	return append(rs, builderWriteRoutes(d)...)
}

// builderWriteRoutes are the change routes and the registry writes: reads under tachyon.builder.read,
// writes under tachyon.builder.write with the operator check.
func builderWriteRoutes(d builderDeps) []routes.Route {
	const read, write = "tachyon.builder.read", "tachyon.builder.write"
	operator := func(h http.Handler) http.Handler { return assurance.Middleware(builderWriteAction, h) }
	h := &builderchange.HTTP{Service: func() *builderchange.Service { return d.changes }, Principal: func(r *http.Request) string {
		if p, ok := libauth.FromContext(r.Context()); ok {
			return p.ID
		}
		return ""
	}}
	// Off, the change routes answer as the old build did unconfigured; capabilities answers it itself.
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d.changes == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"code":"changes_unavailable","message":"Builder changes are not configured on this host."}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	mux := http.NewServeMux()
	h.MountCapabilities(mux, func(n http.Handler) http.Handler { return n })
	h.Mount(mux, gate, gate)
	d.reader.MountWrites(mux, func(n http.Handler) http.Handler { return n })
	p, g := builderapi.Prefix, builderapi.RegistryPrefix
	var rs []routes.Route
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, p + "/capabilities"}, {http.MethodGet, p + "/changes"}, {http.MethodGet, p + "/changes/{id}"},
		{http.MethodGet, p + "/changes/{id}/bundle"},
	} {
		rs = append(rs, routes.Route{Method: r.method, Pattern: r.path, Scope: read, Handler: mux})
	}
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, p + "/changes"}, {http.MethodPost, p + "/changes/{id}/submit"}, {http.MethodPost, p + "/changes/{id}/cancel"},
		{http.MethodPost, p + "/changes/{id}/rearm"}, {http.MethodPost, p + "/changes/{id}/mark_planned"}, {http.MethodPost, p + "/changes/{id}/check"},
		{http.MethodPost, p + "/launches/{id}/launch"},
		{http.MethodPost, g + "/projects"}, {http.MethodPatch, g + "/projects/{id}"}, {http.MethodPost, g + "/projects/{id}/link"},
		{http.MethodDelete, g + "/projects/{id}/link"},
	} {
		rs = append(rs, routes.Route{Method: r.method, Pattern: r.path, Scope: write, Handler: operator(mux)})
	}
	return rs
}

// The old API cache's defaults for the imported team snapshot: kept a week, the catalog policy's 60 s
// fresh and 3600 s revalidate. A policy file's overrides come with the API cache slice.
const (
	importedTeamsKey       = "manual-import:tether:team-definitions:v1"
	cacheRetention         = 7 * 24 * time.Hour
	catalogFreshSeconds    = 60
	catalogRevalidateSecs  = 3600
	importedTeamsEmptyBody = `{"source":"","imported_at":"","teams":[]}`
)

// importedTeams serves the team snapshot an operator imported into the old API cache (the old
// apicache.ImportedTeamsHandler): the stored body, or an empty snapshot when there is none or it has
// expired. It never reads upstream.
func importedTeams(db *store.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		var contentType string
		var updated int64
		err := db.Reader.QueryRowContext(r.Context(), `SELECT body, content_type, updated_at FROM api_cache WHERE key = ?`, importedTeamsKey).Scan(&body, &contentType, &updated)
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Tachyon-Source", "catalog")
		if errors.Is(err, sql.ErrNoRows) || err == nil && time.Since(time.UnixMilli(updated)) > cacheRetention {
			h.Set("Content-Type", "application/json")
			h.Set("X-Tachyon-Cache", "cached")
			h.Set("X-Tachyon-Cache-Enabled", "false")
			_, _ = w.Write([]byte(importedTeamsEmptyBody))
			return
		}
		if err != nil {
			h.Set("Content-Type", "application/json")
			h.Set("X-Tachyon-Cache", "error")
			h.Set("X-Tachyon-Cache-Error", "storage")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"storage","message":"Could not read the imported team snapshot."}`))
			return
		}
		h.Set("Content-Type", contentType)
		h.Set("X-Tachyon-Cache", "imported")
		h.Set("X-Tachyon-Updated-At", time.UnixMilli(updated).UTC().Format(time.RFC3339Nano))
		h.Set("X-Tachyon-Fresh-Seconds", fmt.Sprint(catalogFreshSeconds))
		h.Set("X-Tachyon-Revalidate-Seconds", fmt.Sprint(catalogRevalidateSecs))
		h.Set("X-Tachyon-Cache-Enabled", "true")
		_, _ = w.Write(body)
	})
}
