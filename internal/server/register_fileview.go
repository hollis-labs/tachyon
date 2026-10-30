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

// mountFileviewFiles serves the real POST /stat and GET /content of the file viewer (V3). One pattern each, so
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
	(&fileview.Files{
		Guard: &fileview.Guard{Svc: svc},
		Audit: s.fileviewAudit,
		Principal: func(r *http.Request) string {
			c, err := r.Cookie(approvals.CookieName)
			if err != nil {
				return ""
			}
			sum := sha256.Sum256([]byte(c.Value))
			return "browser-session:" + hex.EncodeToString(sum[:])
		},
	}).Mount(mux, operator.RequireOperatorReads)
}
