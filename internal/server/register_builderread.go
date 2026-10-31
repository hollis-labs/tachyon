package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/builderchange"
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
	// Registry writes use the control token (TACHYON_TETHER_CONTROL_TOKEN_FILE) and need its registry.write scope;
	// without the token file the write routes answer 501.
	if tf := strings.TrimSpace(os.Getenv("TACHYON_TETHER_CONTROL_TOKEN_FILE")); tf != "" {
		if base, client, err := builderchange.NewControlClient(controlAddr(s.cfg.Upstreams.TetherAddr), tf); err == nil {
			reader.Writer = builderread.NewProjectWriter(base, client)
		}
	}
	reader.Mount(mux, guard)
	reader.MountWrites(mux, guard)
}
