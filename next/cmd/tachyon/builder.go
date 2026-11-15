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
	"time"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/builderapi"
	"github.com/hollis-labs/tachyon/next/internal/builderread"
	"github.com/hollis-labs/tachyon/next/internal/catalogindex"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/tetherpass"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// The builder and catalog domain, read side (slice B9a): the catalog index's summary and stream, the
// Builder's agents, launches and library reads, the registry's projects, the team policy and the imported
// team snapshot, from the migrated tables. The catalog index loop runs only with
// tachyon.features.catalog_index; builder changes, registry writes and team operations are slice B9b.

// The catalog reads the Builder uses (the old console plugin and API cache paths).
const (
	teamPolicyPath    = "/api/plugins/console/v1/catalog/team-policy"
	importedTeamsPath = "/api/plugins/console/v1/catalog/imported-teams"
)

type builderDeps struct {
	index  *catalogindex.Index
	reader *builderread.Reader
	tether *tetherpass.Proxy
	db     *store.Store
	start  func(ctx context.Context) (stop func())
}

// newBuilder builds the read side without starting anything. The live session counts and the registry
// read a configured Tether on demand, as before.
func newBuilder(app *module.App, cfg settings.Settings, domain *store.Store) builderDeps {
	resolver := secrets.Resolver(app)
	token := upstreamauth.FromRef(resolver, cfg.Upstreams.Tether.Token)
	idx := &catalogindex.Index{Store: &catalogindex.Store{DB: domain}, Bus: catalogindex.NewBroker(time.Now)}
	d := builderDeps{index: idx, reader: &builderread.Reader{Index: idx.Store}, tether: tetherpass.New(cfg.Upstreams.Tether.URL, token), db: domain}
	if cfg.Upstreams.Tether.URL != "" {
		if sess, err := builderread.NewTetherSessions(cfg.Upstreams.Tether.URL, token); err == nil {
			d.reader.Sessions = sess
		}
		if projects, err := builderread.NewTetherProjects(cfg.Upstreams.Tether.URL, token); err == nil {
			d.reader.Projects = projects
		}
	}
	d.start = func(ctx context.Context) func() {
		if !cfg.Features.CatalogIndex || strings.TrimSpace(cfg.Upstreams.Tether.URL) == "" {
			return func() {}
		}
		src, err := catalogindex.NewHTTPSource(cfg.Upstreams.Tether.URL, token)
		if err != nil {
			slog.Warn("catalog index is enabled but not configured; staying off", "err", err)
			return func() {}
		}
		ctx, cancel := context.WithCancel(ctx)
		idx.Source = src
		idx.Start(ctx)
		return func() { cancel(); idx.Wait(context.Background()) }
	}
	return d
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
