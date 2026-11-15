package main

import (
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/tachyon/next/internal/assurance"
	"github.com/hollis-labs/tachyon/next/internal/routes"
	"github.com/hollis-labs/tachyon/next/internal/settings"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/transcripts"
	"github.com/hollis-labs/tachyon/next/internal/upstreamauth"
)

// The transcripts domain (slice B10): bounded read-only views of Fragments Engine for a browser
// session. tachyon.features.transcripts (default off) gates every outbound read; off, only the saved
// snapshot is served.

// transcriptsReadRoutes are the transcript reads under transcripts.Prefix.
var transcriptsReadRoutes = []string{
	"/snapshot", "/status", "/search", "/stats", "/sessions/{id}", "/sessions/{id}/find", "/sessions/{id}/errors",
}

// newTranscripts is the guarded bridge to Fragments Engine (tachyon.upstreams.fragments), saving its
// snapshot in the domain store.
func newTranscripts(app *module.App, cfg settings.Settings, domain *store.Store) http.Handler {
	token := upstreamauth.FromRef(secrets.Resolver(app), cfg.Upstreams.Fragments.Token)
	b := transcripts.New(cfg.Upstreams.Fragments.URL, token, cfg.Features.Transcripts)
	b.DB = domain
	b.MaxSessions = cfg.Transcripts.MaxSessions // tachyon.transcripts.max_sessions (default 5000)
	return transcriptsGuard(app.Name+"_session", b)
}

// transcriptsRoutes are the transcript routes, each under tachyon.transcripts.read (docs/api-contract.md).
func transcriptsRoutes(h http.Handler) []routes.Route {
	if h == nil {
		return nil
	}
	rs := make([]routes.Route, 0, len(transcriptsReadRoutes))
	for _, p := range transcriptsReadRoutes {
		rs = append(rs, routes.Route{Method: http.MethodGet, Pattern: transcripts.Prefix + p, Scope: "tachyon.transcripts.read", Handler: h})
	}
	return rs
}

// transcriptsGuard keeps transcripts for a browser session, after the route's scope check.
//
// The old guard (internal/server transcriptsGuard) refused any request carrying an Authorization header
// (403 forbidden, "Transcripts are for a browser session.", Cache-Control no-store): an agent bearer
// token has no transcript surface. It then required assurance transcripts.read (browser-session) from
// the Tangent session bridge, writing the refusal as flat {code, message}.
//
// Here the identity middleware turns the session cookie into "Authorization: Bearer <cookie value>"
// before the guard sees the request, so the header alone no longer tells a browser from a machine, and
// neither does the principal: a token the session mints for itself carries the same session id. A bearer
// is the browser session's only when it is exactly the session cookie; any other bearer (an API key, a
// minted token) is refused as before. Then assurance.Require: a signed-in person is browser-session.
func transcriptsGuard(cookie string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refuse := func(status int, code, message string) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
		}
		if h := r.Header.Get("Authorization"); h != "" && !fromSessionCookie(r, cookie, h) {
			refuse(http.StatusForbidden, "forbidden", "Transcripts are for a browser session.")
			return
		}
		if e := assurance.Require(r, "transcripts.read"); e != nil {
			refuse(e.Status, e.Code, e.Message)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// fromSessionCookie reports whether the Authorization header is the bearer the identity middleware
// made from the session cookie.
func fromSessionCookie(r *http.Request, cookie, header string) bool {
	c, err := r.Cookie(cookie)
	return err == nil && c.Value != "" && header == "Bearer "+c.Value
}
