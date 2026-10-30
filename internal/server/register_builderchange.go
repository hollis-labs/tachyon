package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/builderapi"
	"github.com/hollis-labs/tachyon/internal/builderchange"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

var builderChanges atomic.Pointer[builderchange.Service]

func init() {
	registerStart(startBuilderChange)
	registerServerMount(mountBuilderChange)
}

// startBuilderChange builds the change service (B3) when the catalog index is on. With a Tether control token
// file (TACHYON_TETHER_CONTROL_TOKEN_FILE: catalog.write; it never holds the approval passphrase;
// TACHYON_TETHER_CONTROL_ADDR optionally points it at Tether's local listener) it plans and applies itself and
// watches awaiting plans for the host's approval. Without one it runs in handoff mode on the read token.
// With neither the change routes answer 503.
func startBuilderChange(ctx context.Context, s *Server, db *localstore.Store) {
	if s.cfg.BuilderStubs || s.catalogIndex == nil {
		return
	}
	// With a control token the service plans and applies itself (direct). Without one, which is the default,
	// it runs in handoff mode on the read token: changes end at a bundle the operator plans in Tether and
	// "applied" is detected from file shas. Without a read token the routes answer 503.
	mode, tf := builderapi.ModeDirect, os.Getenv("TACHYON_TETHER_CONTROL_TOKEN_FILE")
	addr := controlAddr(s.cfg.Upstreams.TetherAddr)
	if tf == "" {
		mode, tf, addr = builderapi.ModeHandoff, s.cfg.Upstreams.TetherTokenFile, s.cfg.Upstreams.TetherAddr
	}
	if tf == "" {
		slog.Info("builder changes are off: no Tether token file is set")
		return
	}
	t, err := builderchange.NewTether(addr, tf)
	if err != nil {
		slog.Warn("builder changes are off", "err", err)
		return
	}
	if mode == builderapi.ModeHandoff {
		t = builderchange.ReadOnly(t)
	}
	svc := &builderchange.Service{Mode: mode, DB: db, Tether: t, Index: s.catalogIndex.Store, Bus: s.catalogIndex.Bus}
	builderChanges.Store(svc)
	go svc.Run(ctx)
}

// controlAddr is where the change service reaches Tether. Tether's plan-write routes answer only on its local
// listener, so TACHYON_TETHER_CONTROL_ADDR (for example http://127.0.0.1:8948) can point the control client
// there while reads keep TETHER_ADDR. It is used by builderchange.NewTether only; unset means TETHER_ADDR.
func controlAddr(tetherAddr string) string {
	if a := strings.TrimSpace(os.Getenv("TACHYON_TETHER_CONTROL_ADDR")); a != "" {
		return a
	}
	return tetherAddr
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
	h.MountCapabilities(mux, operator.RequireOperator) // answers "unavailable" itself, so it is outside the gate
	h.Mount(mux, func(n http.Handler) http.Handler { return operator.RequireOperator(gate(n)) },
		func(n http.Handler) http.Handler { return operator.RequireOperator(gate(n)) })
}
