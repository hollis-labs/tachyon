package forgeindex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/forgeapi"
)

// Triage is the state of one PR row from the shared item-state store.
type Triage struct {
	Status        string // active | snoozed | dismissed | resolved
	SnoozeUntilMS int64
	By            string
}

// API serves GET /pulls/pending, /status and /stream under forgeapi.Prefix. Until the first refresh
// completes (or when there is no credential) the pending route passes to Fallback, the legacy
// handler, so the page never goes empty during start-up.
type API struct {
	Index *Index
	// Triage returns the state by PR key ("github:o/n#1"); rows without one are active.
	Triage   func(ctx context.Context) (map[string]Triage, error)
	Fallback http.Handler
}

func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

// Routes are the patterns this handler serves.
func Routes() []string {
	return []string{"GET " + forgeapi.Prefix + "/pulls/pending", "GET " + forgeapi.Prefix + "/status", "GET " + forgeapi.Prefix + "/stream"}
}

func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET "+forgeapi.Prefix+"/pulls/pending", a.pending)
	m.HandleFunc("GET "+forgeapi.Prefix+"/status", a.status)
	m.HandleFunc("GET "+forgeapi.Prefix+"/stream", a.stream)
	return m
}

func (a *API) pending(w http.ResponseWriter, r *http.Request) {
	if !a.Index.Ready(r.Context()) && a.Fallback != nil {
		a.Fallback.ServeHTTP(w, r)
		return
	}
	q := r.URL.Query()
	for k := range q {
		if !slices.Contains([]string{"reason", "repo", "state", "q", "limit", "cursor"}, k) {
			fail(w, 400, "invalid_args", "Unknown parameter "+strconv.Quote(k)+".")
			return
		}
	}
	if v := q.Get("reason"); v != "" && !slices.Contains(forgeapi.Reasons, v) {
		fail(w, 400, "invalid_args", "Unknown reason.")
		return
	}
	state := q.Get("state")
	if state == "" {
		state = "active"
	}
	if !slices.Contains([]string{"active", "snoozed", "dismissed", "resolved"}, state) {
		fail(w, 400, "invalid_args", "state must be active, snoozed, dismissed or resolved.")
		return
	}
	limit := forgeapi.PendingPage
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > forgeapi.MaxPendingDefault {
			fail(w, 400, "invalid_args", "limit must be 1 to 100.")
			return
		}
		limit = n
	}
	start := 0
	if c := q.Get("cursor"); c != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(c, "c"))
		if err != nil || !strings.HasPrefix(c, "c") || n < 0 {
			fail(w, 400, "invalid_args", "Unknown cursor.")
			return
		}
		start = n
	}
	snap, err := a.Index.Snapshot(r.Context())
	if err != nil {
		fail(w, 500, "unavailable", "The pull request index is unavailable.")
		return
	}
	tri := map[string]Triage{}
	if a.Triage != nil {
		if tri, err = a.Triage(r.Context()); err != nil {
			fail(w, 500, "unavailable", "The triage state is unavailable.")
			return
		}
	}
	text := strings.ToLower(q.Get("q"))
	now := a.Index.now()
	items := []map[string]any{}
	for _, row := range snap.Rows {
		t, ok := tri[row.PRKey]
		if !ok {
			t = Triage{Status: "active"}
		}
		if t.Status != state {
			continue
		}
		if v := q.Get("reason"); v != "" && !slices.Contains(row.Reasons, v) {
			continue
		}
		if v := q.Get("repo"); v != "" && v != row.Ref.Repo.Owner+"/"+row.Ref.Repo.Name {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(row.Title+" "+row.PRKey), text) {
			continue
		}
		raw, _ := json.Marshal(row)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		tj := map[string]any{"status": t.Status}
		if t.SnoozeUntilMS > 0 {
			tj["snooze_until"] = t.SnoozeUntilMS
		}
		if t.By != "" {
			tj["by"] = t.By
		}
		m["triage"] = tj
		if ut, err := time.Parse(time.RFC3339, row.UpdatedAt); err == nil {
			m["age_s"] = int64(now.Sub(ut).Seconds())
		}
		m["cached"] = true
		m["fetched_ms"] = snap.FetchedMS
		items = append(items, m)
	}
	total := len(items)
	if start > total {
		start = total
	}
	end := min(start+limit, total)
	var next any
	if end < total {
		next = "c" + strconv.Itoa(end)
	}
	st := a.Index.Status(r.Context())
	send(w, 200, map[string]any{"items": items[start:end], "next_cursor": next, "total": total, "cached": st.Degraded, "fetched_ms": snap.FetchedMS, "degraded": st.Degraded})
}

// Status is GET /status.
type Status struct {
	TokenOwner    string         `json:"token_owner"`
	Scopes        []string       `json:"scopes"`
	ScopeWarning  string         `json:"scope_warning,omitempty"`
	Rate          map[string]any `json:"rate"`
	Watched       []string       `json:"watched"`
	LastRefreshMS int64          `json:"last_refresh_ms"`
	Degraded      bool           `json:"degraded"`
	Note          string         `json:"note"`
}

func (x *Index) Status(ctx context.Context) Status {
	rem, reset, known := x.GH.Rate()
	x.mu.Lock()
	viewer, degraded, note := x.viewer, x.degraded, x.note
	x.mu.Unlock()
	s := x.settings(ctx)
	st := Status{TokenOwner: viewer, Scopes: x.GH.Scopes(), Rate: map[string]any{"remaining": rem, "reset_ms": reset}, Watched: s.Watched, Degraded: degraded, Note: note}
	if st.Scopes == nil {
		st.Scopes = []string{}
	}
	if st.Watched == nil {
		st.Watched = []string{}
	}
	if !known {
		st.Rate = map[string]any{"remaining": nil, "reset_ms": nil}
	}
	if slices.Contains(st.Scopes, "repo") {
		st.ScopeWarning = "The token has the broad classic repo scope. Prefer a fine-grained token limited to the watched repos."
	}
	var v string
	if x.DB.Reader.QueryRowContext(ctx, `SELECT v FROM forge_index_state WHERE k = 'last_refresh_ms'`).Scan(&v) == nil {
		st.LastRefreshMS, _ = strconv.ParseInt(v, 10, 64)
	}
	return st
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	send(w, 200, a.Index.Status(r.Context()))
}

func writeEvent(w http.ResponseWriter, e Event) {
	env := map[string]any{"type": e.Type, "at": e.At}
	for k, v := range e.Data {
		env[k] = v
	}
	body, _ := json.Marshal(env)
	if e.ID != 0 {
		fmt.Fprintf(w, "id: %d\n", e.ID)
	}
	fmt.Fprintf(w, "data: %s\n\n", body)
}

// stream is GET /stream: a fresh connection gets the current status; Last-Event-ID or ?after= replays
// what was missed, or one resync when the id cannot be honoured. ?once=1 ends after the replay.
func (a *API) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "unavailable", "Streaming is unsupported here.")
		return
	}
	var last int64
	given := false
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("after")
	}
	if v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, "invalid_args", "Last-Event-ID must be a non-negative integer.")
			return
		}
		last, given = n, true
	}
	bus := a.Index.Bus
	ch := bus.subscribe()
	defer bus.unsubscribe(ch)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	seen := last
	switch {
	case !given:
		st := a.Index.Status(r.Context())
		writeEvent(w, Event{Type: "status", At: a.Index.now().UnixMilli(), Data: map[string]any{"rate": st.Rate, "degraded": st.Degraded}})
		seen = bus.Head()
	default:
		evs, honoured := bus.Since(last)
		if !honoured {
			writeEvent(w, Event{ID: bus.Head(), Type: "resync", At: a.Index.now().UnixMilli(), Data: map[string]any{"reason": "gap"}})
			seen = bus.Head()
		}
		for _, e := range evs {
			writeEvent(w, e)
			seen = e.ID
		}
	}
	fl.Flush()
	if r.URL.Query().Get("once") == "1" {
		return
	}
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.ID <= seen {
				continue
			}
			writeEvent(w, e)
			fl.Flush()
		case <-keep.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}
