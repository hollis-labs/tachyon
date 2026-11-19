package server

import (
	"net/http"
	"os"

	"github.com/hollis-labs/tachyon/internal/account"
	"github.com/hollis-labs/tachyon/internal/approvals"
)

// mountAccount mounts the operator's whoami and local profile (ST3). The settings stub owns the same routes
// when TACHYON_SETTINGS_STUBS=1, so the real mount stands down then.
func (s *Server) mountAccount(mux *http.ServeMux, operator *approvals.Bridge) {
	if s.store == nil || os.Getenv("TACHYON_SETTINGS_STUBS") == "1" {
		return
	}
	(&account.Service{DB: s.store, Provider: &approvals.TangentProvider{Bridge: operator}}).Mount(mux)
}
