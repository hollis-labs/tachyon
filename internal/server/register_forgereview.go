package server

import (
	"net/http"
	"strings"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/forgereview"
)

func init() { registerServerMount(mountForgeReview) }

// mountForgeReview serves review validation, review submission and review drafts (PR6) with the shared
// GitHub token. Every route is explicit, so it wins over the legacy console plugin's catch-all (whose review
// route this replaces). Writes sit behind the operator route guard and go through the API cache so the forge
// reads are invalidated; the draft read passes the guard like any GET and is never cached. The forge stub
// owns the same patterns under TACHYON_FORGE_STUBS=1.
func mountForgeReview(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if forgeStubsOn() {
		return
	}
	h := &forgereview.Handler{Token: s.cfg.Upstreams.GitHubToken, DB: s.store}
	guarded := operator.RequireOperator(h.Mux())
	reviewed := operator.RequireOperator(s.forgeAudit().Wrap("review", h.Mux()))
	write := guarded
	if s.cache != nil {
		write = s.cache.Wrap(guarded)
	}
	for _, route := range forgereview.Routes {
		if strings.HasPrefix(route, "GET ") {
			mux.Handle(route, guarded)
		} else {
			w := write
			if route == "POST "+strings.SplitN(forgereview.Routes[1], " ", 2)[1] {
				w = reviewed
				if s.cache != nil {
					w = s.cache.Wrap(reviewed)
				}
			}
			mux.Handle(route, w)
		}
	}
}
