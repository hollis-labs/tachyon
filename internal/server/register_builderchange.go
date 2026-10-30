package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/builderchange"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

var builderChanges atomic.Pointer[builderchange.Service]

func init() {
	registerStart(startBuilderChange)
	registerServerMount(mountBuilderChange)
}

// startBuilderChange builds the change service (B3) when the catalog index is on and a Tether control token
// file is named (TACHYON_TETHER_CONTROL_TOKEN_FILE: catalog.write; it never holds the approval passphrase),
// and watches awaiting plans for the host's approval. Without it the change routes answer 503.
func startBuilderChange(ctx context.Context, s *Server, db *localstore.Store) {
	if s.cfg.BuilderStubs || s.catalogIndex == nil {
		return
	}
	tf := os.Getenv("TACHYON_TETHER_CONTROL_TOKEN_FILE")
	if tf == "" {
		slog.Info("builder changes are off: TACHYON_TETHER_CONTROL_TOKEN_FILE is not set")
		return
	}
	t, err := builderchange.NewTether(s.cfg.Upstreams.TetherAddr, tf)
	if err != nil {
		slog.Warn("builder changes are off", "err", err)
		return
	}
	svc := &builderchange.Service{DB: db, Tether: t, Index: s.catalogIndex.Store, Bus: s.catalogIndex.Bus}
	builderChanges.Store(svc)
	go svc.Run(ctx)
}

func mountBuilderChange(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if s.cfg.BuilderStubs || s.catalogIndex == nil || s.store == nil {
		return
	}
	principal := func(r *http.Request) string {
		c, err := r.Cookie(approvals.CookieName)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256([]byte(c.Value))
		return "browser-session:" + hex.EncodeToString(sum[:])
	}
	// The service is built at start; the handlers look it up per request so mount order does not matter.
	h := &builderchange.HTTP{Principal: principal, Service: builderChanges.Load}
	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if builderChanges.Load() == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"code":"changes_unavailable","message":"Builder changes are not configured on this host."}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	h.Mount(mux, func(n http.Handler) http.Handler { return operator.RequireOperator(gate(n)) },
		func(n http.Handler) http.Handler { return operator.RequireOperator(gate(n)) })
}
