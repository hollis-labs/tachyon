package dataverbs

import (
	"net/http"
	"strings"

	"github.com/hollis-labs/libs/core/util/svcerr"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tachyon/next/internal/assurance"
)

// NewConsoleHTTP serves the data verbs to signed-in people at Prefix. Each verb needs its console
// scope (ConsoleScope) on a person; an API key is refused here whatever its scopes (agents use the
// agent API, where their grant and kinds apply). Mutating verbs need the operator check in addition;
// none is served yet.
func NewConsoleHTTP(local *Local) http.Handler {
	h := newHTTP(Prefix, []Provider{local}, func(next http.Handler) http.Handler {
		return assurance.Middleware("settings.write_operator", next)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := libauth.FromContext(r.Context())
		if assurance.Of(p) < assurance.BrowserSession {
			writeError(w, svcerr.New(svcerr.CodePermission, "The console data routes answer signed-in people; agents use the agent API.", svcerr.WithStatus(http.StatusForbidden)))
			return
		}
		// The verb is read from the path itself, not from a mux pattern value, so the check holds
		// however the route is mounted.
		if rel, ok := strings.CutPrefix(r.URL.Path, Prefix+"/providers/"); ok {
			parts := strings.Split(rel, "/")
			verb := parts[len(parts)-1]
			scope, ok := ConsoleScope(verb)
			if !ok {
				writeError(w, svcerr.New(svcerr.CodeNotFound, "Unknown data verb."))
				return
			}
			if !p.HasScope(scope) {
				writeError(w, svcerr.New("scope_required", "required scope: "+scope, svcerr.WithStatus(http.StatusForbidden)))
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}
