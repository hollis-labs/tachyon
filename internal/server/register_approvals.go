package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/approvalsapi"
)

func init() { registerFeature(mountApprovalsStubs) }

// mountApprovalsStubs serves the fixture-backed Approvals contract (TACHYON_APPROVALS_STUBS=1) route
// by route: more specific patterns win over the real session routes, which stay with the bridge.
func mountApprovalsStubs(mux *http.ServeMux, operator *approvals.Bridge) {
	if os.Getenv("TACHYON_APPROVALS_STUBS") != "1" {
		return
	}
	stub := approvalsapi.Stub{}.Handler()
	for _, route := range approvalsapi.Routes {
		method, path, _ := strings.Cut(route, " ")
		mux.Handle(method+" "+approvalsapi.Prefix+path, operator.RequireOperator(stub))
	}
}
