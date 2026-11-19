package server

import (
	"net/http"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/forgereads"
)

func init() { registerServerMount(mountForgeReads) }

// mountForgeReads serves the PR4 read endpoints (files, diff, checks, commit ref, viewer) with the
// shared GitHub token. Each pattern is more specific than the legacy console plugin's catch-all, so it
// wins without a duplicate registration; GETs pass RequireOperator untouched. The forge stub owns the
// same patterns under TACHYON_FORGE_STUBS=1, so this stands down.
func mountForgeReads(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if forgeStubsOn() {
		return
	}
	h := operator.RequireOperator((&forgereads.Handler{Token: s.cfg.Upstreams.GitHubToken}).Mux())
	for _, route := range forgereads.Routes {
		mux.Handle(route, h)
	}
}
