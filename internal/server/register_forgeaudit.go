package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/forgeapi"
	"github.com/hollis-labs/tachyon/internal/forgeaudit"
)

func init() { registerServerMount(mountForgeAudit) }

const forgeAuditKey = "forgeaudit"

// forgeAudit is the one forge write log of the server, created on first use.
func (s *Server) forgeAudit() *forgeaudit.Log {
	if v, ok := s.ext.Load(forgeAuditKey); ok {
		return v.(*forgeaudit.Log)
	}
	l := &forgeaudit.Log{DB: s.store, Principal: func(r *http.Request) string {
		c, err := r.Cookie(approvals.CookieName)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256([]byte(c.Value))
		return "browser-session:" + hex.EncodeToString(sum[:])[:12]
	}}
	v, _ := s.ext.LoadOrStore(forgeAuditKey, l)
	return v.(*forgeaudit.Log)
}

// mountForgeAudit serves GET /forge/audit: who merged, reviewed, assigned or commented on what. Reading it needs
// an operator session. The forge stub owns the same pattern under TACHYON_FORGE_STUBS=1.
func mountForgeAudit(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if forgeStubsOn() || s.store == nil {
		return
	}
	mux.Handle("GET "+forgeapi.Prefix+"/audit", operator.RequireOperatorReads(s.forgeAudit().Handler()))
}
