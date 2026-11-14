// Package pluginallow closes the plugin forwarding prefix to everything that is not listed. The
// pluginhost module forwards any method on /api/plugins/{id}/... to the plugin under one scope; the
// old build forwarded every unmatched console path to Torque with Tachyon's own credential. Here a
// request under /api/plugins/ reaches the host only when its method and path match an entry of the
// allowlist (docs/api-contract.md); anything else is 404, for every caller, the owner included.
//
// The list is empty until the plugin slices add their paths, so in this build no request reaches a
// plugin.
package pluginallow

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Prefix is the forwarding prefix the pluginhost module serves.
const Prefix = "/api/plugins/"

// Entry is one allowed (method, path pattern), in net/http pattern syntax: "GET",
// "/api/plugins/console/v1/tasks/{id}".
type Entry struct {
	Method  string
	Pattern string
}

// Handler serves next for every path outside Prefix and for allowed paths under it.
func Handler(allow []Entry, next http.Handler) http.Handler {
	mux := http.NewServeMux()
	for _, e := range allow {
		mux.Handle(e.Method+" "+e.Pattern, next)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, Prefix) {
			next.ServeHTTP(w, r)
			return
		}
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "not_found", "message": "no such route"})
	})
}
