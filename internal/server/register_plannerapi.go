package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/plannerapi"
)

func init() { registerFeature(mountPlannerStubs) }

// mountPlannerStubs serves the fixture-backed Planner extension contract (TACHYON_PLANNER_STUBS=1) route by
// route; each pattern is more specific than the real planner's subtree mount, so the stub wins without a
// duplicate registration. The real planner keeps every route not listed in plannerapi.Routes.
func mountPlannerStubs(mux *http.ServeMux, operator *approvals.Bridge) {
	if os.Getenv("TACHYON_PLANNER_STUBS") != "1" {
		return
	}
	stub := operator.RequireOperator(plannerapi.Stub{}.Handler())
	for _, route := range plannerapi.Routes {
		method, path, _ := strings.Cut(route, " ")
		mux.Handle(method+" "+plannerapi.Prefix+path, stub)
	}
}
