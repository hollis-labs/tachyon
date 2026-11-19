package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/transcriptsapi"
)

func init() { registerFeature(mountTranscriptsStubs) }

// mountTranscriptsStubs serves the fixture-backed Transcripts contract (TACHYON_TRANSCRIPTS_STUBS=1)
// route by route; each pattern is more specific than the bridge's prefix mount, so the stub wins
// without a duplicate registration. Reads pass; anything else never reaches the stub.
func mountTranscriptsStubs(mux *http.ServeMux, operator *approvals.Bridge) {
	if os.Getenv("TACHYON_TRANSCRIPTS_STUBS") != "1" {
		return
	}
	stub := operator.RequireOperator(transcriptsapi.Stub{}.Handler())
	for _, route := range transcriptsapi.Routes {
		method, path, _ := strings.Cut(route, " ")
		mux.Handle(method+" "+transcriptsapi.Prefix+path, stub)
	}
}
