package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/fileview"
)

func init() { registerServerMount(mountFileviewFiles) }

// mountFileviewFiles serves the real POST /stat, GET /content (V3), POST /frame and GET /frames/{id} (V4) of the file viewer. One pattern each, so
// they win over the stub's subtree when TACHYON_FILEVIEW_STUBS=1. Both need an operator session; reads of file
// bytes are as sensitive as a mutation.
func mountFileviewFiles(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if s.store == nil {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	svc := &fileview.Service{DB: s.store, Home: home}
	guard := &fileview.Guard{Svc: svc}
	principal := func(r *http.Request) string {
		c, err := r.Cookie(approvals.CookieName)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256([]byte(c.Value))
		return "browser-session:" + hex.EncodeToString(sum[:])
	}
	(&fileview.Files{Guard: guard, Audit: s.fileviewAudit, Principal: principal}).Mount(mux, operator.RequireOperatorReads)
	// POST /frame mints a one-time frame and needs an operator session; GET /frames/{id} is the one unguarded
	// route: a sandboxed iframe has an opaque origin and no cookie, so the 256-bit single-use id is the capability.
	(&fileview.Frames{Guard: guard, Audit: s.fileviewAudit, Principal: principal}).Mount(mux, operator.RequireOperator)
}
