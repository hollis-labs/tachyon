package server

import (
	"net/http"
	"strings"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/builderread"
	"github.com/hollis-labs/tachyon/internal/needsyou"
)

func init() { registerServerMount(mountNeedsYou) }

// mountNeedsYou serves GET needs-you (CW-20261114-0023c) behind the operator guard: what waits on @chrispian,
// read live from Tether with the read token and from the collector's seat statuses.
func mountNeedsYou(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	a := &needsyou.Aggregator{}
	if addr := strings.TrimSpace(s.cfg.Upstreams.TetherAddr); addr != "" && s.cfg.Upstreams.TetherTokenFile != "" {
		if base, c, err := builderread.NewTetherClient(addr, s.cfg.Upstreams.TetherTokenFile); err == nil {
			a.Tether = &needsyou.HTTPTether{Base: base, Client: c}
		}
	}
	if s.agentsCollector != nil {
		a.Statuses = s.agentsCollector.Store
	}
	mux.Handle("GET "+needsyou.Path, operator.RequireOperator(a.Handler()))
}
