package needsyou

import (
	"encoding/json"
	"net/http"
)

// Path is GET needs-you: the live list (Result).
const Path = "/api/plugins/console/v1/needs-you"

// Handler serves the view. It always answers 200 with every source's state: an unreadable source is in
// sources[*].error, not an HTTP error, so the page can show what it has and say what it could not read.
func (a *Aggregator) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(a.Read(r.Context()))
	})
}
