package server

import (
	"net/http"
	"os"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/forgeapi"
)

func init() { registerFeature(mountForgeStubs) }

// forgeStubsOn reports TACHYON_FORGE_STUBS=1.
func forgeStubsOn() bool { return os.Getenv("TACHYON_FORGE_STUBS") == "1" }

// mountForgeStubs serves the fixture-backed Pull requests contract under the forge and console-state
// prefixes. GETs are open; every write needs an operator session.
func mountForgeStubs(mux *http.ServeMux, operator *approvals.Bridge) {
	if !forgeStubsOn() {
		return
	}
	h := operator.RequireOperator(forgeapi.Stub{}.Handler())
	mux.Handle(forgeapi.Prefix+"/", h)
	mux.Handle(forgeapi.StatePrefix, h)
	mux.Handle(forgeapi.StatePrefix+"/", h)
}
