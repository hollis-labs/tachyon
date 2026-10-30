package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/forgeapi"
	"github.com/hollis-labs/tachyon/internal/itemstate"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const itemStateKey = "itemstate"

func init() {
	registerStart(startItemState)
	registerServerMount(mountItemState)
}

// startItemState opens the shared console item-state store behind /console/state (PR2) and runs its
// snooze wake loop. Key prefixes are an allow-list; Alerts compat (AL1) adds its own here.
func startItemState(ctx context.Context, s *Server, store *localstore.Store) {
	st := &itemstate.Store{DB: store, Prefixes: []string{"pr-inbox:", "pr-assign:"}}
	s.ext.Store(itemStateKey, st)
	go st.Maintain(ctx, itemstate.WakeInterval, slog.Default().Warn)
}

// mountItemState serves the real /console/state. GETs are open like every console read; PUT and bulk
// need an operator session and refuse agents. With TACHYON_FORGE_STUBS=1 the forge stub owns the prefix.
func mountItemState(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	v, ok := s.ext.Load(itemStateKey)
	if !ok || forgeStubsOn() {
		return
	}
	h := operator.RequireOperator(v.(*itemstate.Store).Handler(forgeapi.StatePrefix))
	mux.Handle(forgeapi.StatePrefix, h)
	mux.Handle(forgeapi.StatePrefix+"/", h)
}
