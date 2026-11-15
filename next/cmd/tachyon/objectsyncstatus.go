package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/objectsync"
)

// objectSyncStatusPath is the old build's read-only sync status route.
const objectSyncStatusPath = "/api/plugins/console/v1/objects/sync/status"

func objectSyncStatus(jobs *objectsync.Jobs) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if jobs == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"unavailable","message":"Object sync has not started."}` + "\n"))
			return
		}
		h, err := jobs.Health(r.Context(), time.Now())
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"unavailable","message":"Object sync state could not be read."}` + "\n"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sources": h, "checked_at": time.Now().UTC().Format(time.RFC3339)})
	})
}
