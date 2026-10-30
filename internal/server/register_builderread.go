package server

import (
	"net/http"

	"github.com/hollis-labs/tachyon/internal/builderread"
)

// mountBuilderRead mounts the Builder read model (B2). Live data needs Tether; without it the routes still
// answer and report live counts as null (the registry routes answer 503 without a registry).
func (s *Server) mountBuilderRead(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	reader := &builderread.Reader{Index: s.catalogIndex.Store}
	if sess, err := builderread.NewTetherSessions(s.cfg.Upstreams.TetherAddr, s.cfg.Upstreams.TetherTokenFile); err == nil {
		reader.Sessions = sess
	}
	if projects, err := builderread.NewTetherProjects(s.cfg.Upstreams.TetherAddr, s.cfg.Upstreams.TetherTokenFile); err == nil {
		reader.Projects = projects
	}
	reader.Mount(mux, guard)
}
