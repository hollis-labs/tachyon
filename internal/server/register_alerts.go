package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/approvals"
)

func init() { registerFeature(mountAlertsStubs) }

// mountAlertsStubs serves the fixture-backed Alerts contract (TACHYON_ALERTS_STUBS=1), one explicit pattern at
// a time. Reads are open; writes need a same-origin browser request (checked in the stub, no operator elevation:
// alert ops are low impact, see the design).
func mountAlertsStubs(mux *http.ServeMux, _ *approvals.Bridge) {
	if os.Getenv("TACHYON_ALERTS_STUBS") != "1" {
		return
	}
	stub := alertsapi.NewStub().Handler()
	for _, route := range alertsapi.Routes {
		method, path, _ := strings.Cut(route.Pattern, " ")
		mux.Handle(method+" "+route.Base+path, stub)
	}
}
