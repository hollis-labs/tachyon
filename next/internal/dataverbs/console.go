package dataverbs

import (
	"net/http"
	"strings"

	"github.com/hollis-labs/libs/core/util/svcerr"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tachyon/next/internal/assurance"
)

// NewConsoleHTTP serves the data verbs to signed-in people at Prefix: the local provider and, as in the
// old build, the Tether provider (actions). Each verb needs its console scope (consoleScope) on a person;
// an API key is refused here whatever its scopes (agents use the agent API, where their grant and kinds
// apply). Mutating verbs need the operator check in addition, as the old RequireOperator did.
func NewConsoleHTTP(local *Local, actions *Actions) http.Handler {
	providers := []Provider{local}
	if actions != nil {
		providers = append(providers, actions)
	}
	h := newHTTP(Prefix, providers, func(next http.Handler) http.Handler {
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
			scope, ok := consoleScope(parts[0], parts[len(parts)-1])
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

// consoleScope is the scope a person needs for a provider's verb on the console. The Tether provider's
// verbs are the objects area (docs/api-contract.md section 5): reads need tachyon.objects.read and
// mutations tachyon.objects.write.
func consoleScope(provider, verb string) (string, bool) {
	switch provider {
	case "tachyon":
		return ConsoleScope(verb)
	case "tether":
		for _, v := range (*Actions)(nil).Verbs() {
			if v.Name == verb {
				if v.Mutates {
					return "tachyon.objects.write", true
				}
				return "tachyon.objects.read", true
			}
		}
	}
	return "", false
}
