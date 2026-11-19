package server

import (
	"net/http"
	"os"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/settingsapi"
	"github.com/hollis-labs/tachyon/internal/settingsreg"
)

const settingsRegKey = "settingsreg"

func init() { registerServerMount(mountSettingsReg) }

// settingsRegistry is the one settings registry of this server, created on first use. Features register their
// groups from their start hooks (settingsRegistry(s).Register(...)) instead of adding private routes. It needs
// the local store, so it is nil without one.
func settingsRegistry(s *Server) *settingsreg.Registry {
	if s.store == nil {
		return nil
	}
	return settingsRegistryOn(s, s.store)
}

func settingsRegistryOn(s *Server, db *localstore.Store) *settingsreg.Registry {
	v, _ := s.ext.LoadOrStore(settingsRegKey, &settingsreg.Registry{DB: db})
	return v.(*settingsreg.Registry)
}

// mountSettingsReg serves the real admin manifest and group routes (ST1) one explicit pattern at a time. The
// handler does its own session, same-origin and per-field operator checks through the assurance Provider, so no
// RequireOperator wrapper (an operator-only wrapper would hide the step-up hint and stop browser-session
// fields being editable). The settings stub owns the same routes under TACHYON_SETTINGS_STUBS=1.
func mountSettingsReg(s *Server, mux *http.ServeMux, operator *approvals.Bridge) {
	if s.store == nil || os.Getenv("TACHYON_SETTINGS_STUBS") == "1" {
		return
	}
	reg := settingsRegistry(s)
	h, err := reg.Handler(&approvals.TangentProvider{Bridge: operator})
	if err != nil {
		return
	}
	mux.Handle("GET "+settingsapi.AdminPrefix+"/manifest", h)
	base := settingsapi.AdminPrefix + "/settings/{id}"
	mux.Handle("GET "+base, h)
	for _, op := range []string{"validate", "update", "reset"} {
		mux.Handle("POST "+base+"/"+op, h)
	}
}
