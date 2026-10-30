package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/forgeindex"
	"github.com/hollis-labs/tachyon/internal/forgemerge"
	"github.com/hollis-labs/tachyon/internal/itemstate"
)

func init() { registerServerMount(mountForgeMerge) }

// mountForgeMerge serves merge preflight (GET, open like the other forge reads) and the guarded merge (PUT,
// behind the operator route guard) with the shared GitHub token. Both patterns are explicit, so they win over
// the legacy console plugin's catch-all; the merge goes through the API cache so the forge reads are
// invalidated. The forge stub owns the same patterns under TACHYON_FORGE_STUBS=1.
func mountForgeMerge(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if forgeStubsOn() {
		return
	}
	h := &forgemerge.Handler{Token: s.cfg.Upstreams.GitHubToken, DB: s.store}
	h.After = func(ctx context.Context, prKey, sha string) forgemerge.Settled { return afterMerge(s, ctx, prKey, sha) }
	guarded := operator.RequireOperator(h.Mux())
	write := guarded
	if s.cache != nil {
		write = s.cache.Wrap(guarded)
	}
	mux.Handle(forgemerge.Routes[0], guarded)
	mux.Handle(forgemerge.Routes[1], write)
}

// afterMerge refreshes the PR index (which publishes pr.merged), settles the PR's triage entry and reports
// whether that worked. Agent links are not stored yet (PR7), so none are closed here.
func afterMerge(s *Server, ctx context.Context, prKey, _ string) forgemerge.Settled {
	if v, ok := s.ext.Load(forgeIndexKey); ok {
		x := v.(*forgeindex.Index)
		go func() {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := x.Refresh(c); err != nil {
				slog.Warn("forge merge: index refresh failed", "err", err)
			}
		}()
	}
	v, ok := s.ext.Load(itemStateKey)
	if !ok {
		return forgemerge.Settled{}
	}
	if _, err := v.(*itemstate.Store).Apply(ctx, "pr-inbox:"+prKey, "operator", itemstate.Op{Type: "settle", Resolution: "merged"}); err != nil {
		slog.Warn("forge merge: cannot settle the triage entry", "err", err)
		return forgemerge.Settled{}
	}
	return forgemerge.Settled{Settled: true}
}
