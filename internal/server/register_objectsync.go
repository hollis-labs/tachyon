package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsource"
	"github.com/hollis-labs/tachyon/internal/alertstore"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/objectsync"
)

// ObjectSyncStatusPath reports each configured object-sync source: schedule, last success, problem, stale.
const ObjectSyncStatusPath = "/api/plugins/console/v1/objects/sync/status"

const objectJobsKey = "objectsync.jobs"

// objectSyncCheckEvery is how often the watchdog looks for a stalled source.
var objectSyncCheckEvery = time.Minute

func init() { registerServerMount(mountObjectSync) }

func (s *Server) loadObjectJobs() *objectsync.Jobs {
	v, ok := s.ext.Load(objectJobsKey)
	if !ok {
		return nil
	}
	return v.(*objectsync.Jobs)
}

// mountObjectSync serves the read-only sync status. Reads are open like the rest of the console reads.
func mountObjectSync(s *Server, mux *http.ServeMux, _ *approvals.Bridge) {
	mux.HandleFunc("GET "+ObjectSyncStatusPath, func(w http.ResponseWriter, r *http.Request) {
		jobs := s.loadObjectJobs()
		if jobs == nil {
			http.Error(w, `{"code":"unavailable","message":"Object sync has not started."}`, http.StatusServiceUnavailable)
			return
		}
		h, err := jobs.Health(r.Context(), time.Now())
		if err != nil {
			http.Error(w, `{"code":"unavailable","message":"Object sync state could not be read."}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"sources": h, "checked_at": time.Now().UTC().Format(time.RFC3339)})
	})
}

// watchObjectSync raises a warning alert when a configured source stops syncing (no enabled schedule, or no
// success within max(3 intervals, objectsync.StallFloor)). One alert per stall: the dedupe key carries the last
// success, so the alert is raised once and raised again only after the source recovers and stalls anew.
func (s *Server) watchObjectSync(ctx context.Context) {
	t := time.NewTicker(objectSyncCheckEvery)
	defer t.Stop()
	for {
		s.checkObjectSync(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) checkObjectSync(ctx context.Context, now time.Time) {
	jobs := s.loadObjectJobs()
	v, ok := s.ext.Load(alertsKey)
	if jobs == nil || !ok {
		return
	}
	api := v.(*alertstore.API)
	health, err := jobs.Health(ctx, now)
	if err != nil {
		slog.Warn("object sync watchdog: health unavailable", "err", err)
		return
	}
	for _, h := range health {
		if !h.Stale {
			continue
		}
		slog.Warn("object sync stalled", "source", h.Name, "reason", h.Reason, "last_success_at", h.LastSuccessAt)
		err := api.Store.Ingest(alertsource.Event{
			Source: "objectsync", SourceEventID: "stalled:" + h.Name + ":" + h.LastSuccessAt + ":" + boolWord(h.Enabled),
			Kind: "sync.stalled", AtMS: now.UnixMilli(), SeverityHint: "warning",
			Subjects:    []alertsapi.Subject{{Kind: "service", Ref: "objectsync:" + h.Name, Label: "Object sync: " + h.Name}},
			CoalesceKey: "objectsync:" + h.Name,
			Payload:     map[string]any{"source": h.Name, "reason": h.Reason, "last_success_at": h.LastSuccessAt, "enabled": h.Enabled},
		})
		if err != nil {
			slog.Warn("object sync watchdog: alert ingest failed", "source", h.Name, "err", err)
		}
	}
}

func boolWord(b bool) string {
	if b {
		return "enabled"
	}
	return "unscheduled"
}
