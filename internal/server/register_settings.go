package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/settingsapi"
)

func init() { registerFeature(mountSettingsStubs) }

// mountSettingsStubs serves the in-memory Settings, Account and Assurance contract (TACHYON_SETTINGS_STUBS=1),
// one explicit pattern at a time. Like the Alerts stub it holds no real data and relies on its own same-origin
// check instead of RequireOperator: the stub is what demonstrates a browser-session write and the step-up
// refusal, so an operator wrapper would hide them. Its access routes live under /settings-stub/ and do not
// touch the real approval-inbox /access.
func mountSettingsStubs(mux *http.ServeMux, _ *approvals.Bridge) {
	if os.Getenv("TACHYON_SETTINGS_STUBS") != "1" {
		return
	}
	stub := settingsapi.NewStub().Handler()
	for _, pattern := range settingsapi.Routes() {
		method, path, _ := strings.Cut(pattern, " ")
		mux.Handle(method+" "+path, stub)
	}
}
