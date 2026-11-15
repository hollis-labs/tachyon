package agentsapi

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed fixtures/*.json openapi.yaml
var assets embed.FS

// OpenAPI is the contract document, served at GET <Prefix>/openapi.yaml by the stub.
func OpenAPI() []byte { b, _ := assets.ReadFile("openapi.yaml"); return b }

// Stub serves the fixtures. Handlers are mounted by pattern, so an unknown path
// is a 404 here and never falls through to the unguarded /api/plugins/ catch-all.
// Writes validate their bodies like the real routes will, but change nothing.
type Stub struct{}

func (Stub) Handler() http.Handler {
	m := http.NewServeMux()
	h := func(pattern string, f http.HandlerFunc) {
		method, path, _ := strings.Cut(pattern, " ")
		m.HandleFunc(method+" "+Prefix+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Tachyon-Agents-Stub", "1")
			// Error-shape tests: X-Stub-Status: 400|404|409|429|503.
			if s, _ := strconv.Atoi(r.Header.Get("X-Stub-Status")); s >= 400 && s < 600 {
				fail(w, s, errCode(s), "Forced by X-Stub-Status.")
				return
			}
			f(w, r)
		})
	}
	h("GET /sessions", func(w http.ResponseWriter, r *http.Request) { sessionsList(w, r) })
	h("GET /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if s, ok := findSession(w, r); ok {
			write(w, 200, s)
		}
	})
	h("GET /sessions/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := findSession(w, r); !ok {
			return
		}
		if r.URL.Query().Get("before_seq") != "" {
			fixture(w, 200, "session_events_page2")
			return
		}
		fixture(w, 200, "session_events")
	})
	h("GET /sessions/{id}/context-series", func(w http.ResponseWriter, r *http.Request) {
		if s, ok := findSession(w, r); ok {
			if ctx, _ := s["context"].(map[string]any); ctx["used"] == nil {
				write(w, 200, map[string]any{"points": []any{}, "compactions": []any{}, "clears": []any{}, "warn_pct": 90})
				return
			}
			fixture(w, 200, "context_series")
		}
	})
	h("GET /sessions/{id}/tokens", func(w http.ResponseWriter, r *http.Request) {
		if s, ok := findSession(w, r); ok {
			if t, _ := s["tokens"].(map[string]any); t["metered"] == false {
				fixture(w, 200, "tokens_unmetered")
				return
			}
			fixture(w, 200, "tokens")
		}
	})
	h("GET /sessions/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := findSession(w, r); ok {
			fixture(w, 200, "history")
		}
	})
	h("GET /bus/events", func(w http.ResponseWriter, r *http.Request) { busEvents(w, r) })
	h("GET /collector", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Stub-Scenario") == "collector_down" {
			fixture(w, 200, "collector_down")
			return
		}
		fixture(w, 200, "collector")
	})
	h("POST /sessions/{id}/nudge", nudge)
	h("POST /sessions/{id}/restart", restart)
	h("POST /controls/{request_id}/skip", func(w http.ResponseWriter, r *http.Request) {
		if c, ok := findControl(w, r); ok {
			if c["kind"] != ControlRestart {
				fail(w, 409, "conflict", "Only a restart can skip its handoff wait.")
				return
			}
			write(w, 200, controlFixtures()["req_restart_stopping"])
		}
	})
	h("GET /controls/{request_id}", func(w http.ResponseWriter, r *http.Request) {
		if c, ok := findControl(w, r); ok {
			write(w, 200, c)
		}
	})
	h("GET /settings", func(w http.ResponseWriter, _ *http.Request) { fixture(w, 200, "settings") })
	h("PUT /settings", putSettings)
	m.HandleFunc("GET "+Prefix+"/stream", serveStream)
	m.HandleFunc("GET "+Prefix+"/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(OpenAPI())
	})
	return m
}

func load(name string, v any) bool {
	b, err := assets.ReadFile("fixtures/" + name + ".json")
	return err == nil && json.Unmarshal(b, v) == nil
}

func fixture(w http.ResponseWriter, status int, name string) {
	b, err := assets.ReadFile("fixtures/" + name + ".json")
	if err != nil {
		fail(w, 500, "internal", "missing fixture "+name)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	write(w, status, map[string]string{"code": code, "message": msg})
}

func errCode(s int) string {
	switch s {
	case 400:
		return "invalid"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 429:
		return "rate_limited"
	case 503:
		return "not_configured"
	}
	return "internal"
}

type sessionsDoc struct {
	Sessions  []map[string]any `json:"sessions"`
	Summary   map[string]any   `json:"summary"`
	Collector map[string]any   `json:"collector"`
}

func sessionsList(w http.ResponseWriter, r *http.Request) {
	var d sessionsDoc
	if !load("sessions", &d) {
		fail(w, 500, "internal", "missing fixture sessions")
		return
	}
	q := r.URL.Query()
	state, text := q.Get("state"), strings.ToLower(q.Get("q"))
	if state != "" {
		switch state {
		case StateWorking, StateWaiting, StateIdle, StateStale, StateUnknown:
		default:
			fail(w, 400, "invalid", "state must be working, waiting, idle, stale or unknown.")
			return
		}
	}
	out := d.Sessions[:0:0]
	for _, s := range d.Sessions {
		if state != "" && s["state"] != state {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(fmt.Sprint(s["agent"], s["workroot"], s["provider"])), text) {
			continue
		}
		out = append(out, s)
	}
	d.Sessions = out // summary stays global: it counts every session
	if r.Header.Get("X-Stub-Scenario") == "collector_down" {
		load("collector_down", &d.Collector)
	}
	write(w, 200, d)
}

func findSession(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var d sessionsDoc
	load("sessions", &d)
	id := r.PathValue("id")
	for _, s := range d.Sessions {
		if s["id"] == id {
			return s, true
		}
	}
	fail(w, 404, "not_found", "No such session.")
	return nil, false
}

func controlFixtures() map[string]map[string]any {
	m := map[string]map[string]any{}
	load("controls", &m)
	return m
}

func findControl(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	if c, ok := controlFixtures()[r.PathValue("request_id")]; ok {
		return c, true
	}
	fail(w, 404, "not_found", "No such control request.")
	return nil, false
}

func busEvents(w http.ResponseWriter, r *http.Request) {
	var d struct {
		Events     []map[string]any `json:"events"`
		NextBefore *int64           `json:"next_before"`
	}
	load("bus_events", &d)
	t := r.URL.Query().Get("type")
	if t != "" {
		// UI filter names (deploys, restarts, tasks, failures) and the event types are both accepted.
		t = strings.TrimSuffix(t, "s")
		if t == "failure" || t == "deploy" || t == "restart" || t == "task" || t == "host" || t == "lifecycle" || t == "card" || t == "workstream" {
			out := d.Events[:0:0]
			for _, e := range d.Events {
				if e["type"] == t {
					out = append(out, e)
				}
			}
			d.Events = out
		} else {
			fail(w, 400, "invalid", "type must be deploys, restarts, tasks or failures.")
			return
		}
	}
	write(w, 200, d)
}

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || json.Unmarshal(b, v) != nil {
		fail(w, 400, "invalid", "Body must be a JSON object.")
		return false
	}
	return true
}

func nudge(w http.ResponseWriter, r *http.Request) {
	s, ok := findSession(w, r)
	if !ok {
		return
	}
	var b struct {
		RequestID string `json:"request_id"`
		Text      string `json:"text"`
		Interrupt bool   `json:"interrupt"`
	}
	if !readBody(w, r, &b) {
		return
	}
	if b.RequestID == "" || strings.TrimSpace(b.Text) == "" || utf8.RuneCountInString(b.Text) > MaxNudgeText {
		fail(w, 400, "invalid", "request_id and text (1 to 4000 characters) are required.")
		return
	}
	if caps, _ := s["capabilities"].(map[string]any); !boolOK(caps["nudge"]) {
		fail(w, 409, "nudge_unsupported", "This session cannot receive nudges.")
		return
	}
	if b.Interrupt {
		caps, _ := s["capabilities"].(map[string]any)
		in, _ := caps["interrupt"].(map[string]any)
		if !boolOK(in["available"]) {
			// Never silently downgrade: the client offers "send without interrupt".
			fail(w, 409, "interrupt_unsupported", fmt.Sprint(in["reason"]))
			return
		}
		fixture(w, 202, "nudge_interrupted")
		return
	}
	fixture(w, 202, "nudge_delivered")
}

func restart(w http.ResponseWriter, r *http.Request) {
	s, ok := findSession(w, r)
	if !ok {
		return
	}
	var b struct {
		RequestID         string `json:"request_id"`
		Handoff           bool   `json:"handoff"`
		ExpectedSessionID string `json:"expected_session_id"`
	}
	if !readBody(w, r, &b) {
		return
	}
	if b.RequestID == "" || b.ExpectedSessionID == "" {
		fail(w, 400, "invalid", "request_id and expected_session_id are required.")
		return
	}
	if b.ExpectedSessionID != s["id"] {
		fail(w, 409, "stale_session", "expected_session_id is not the agent's current session.")
		return
	}
	if caps, _ := s["capabilities"].(map[string]any); !boolOK(caps["restart"]) {
		fail(w, 409, "restart_unsupported", "This session cannot be restarted.")
		return
	}
	fixture(w, 202, "restart_requested")
}

func putSettings(w http.ResponseWriter, r *http.Request) {
	var b struct {
		StaleAfter  int      `json:"stale_after_s"`
		Prompt      string   `json:"handoff_prompt"`
		Timeout     int      `json:"handoff_timeout_s"`
		NudgePreset []string `json:"nudge_presets"`
	}
	if !readBody(w, r, &b) {
		return
	}
	if b.StaleAfter < 60 || b.Timeout < 10 || b.Timeout > 1800 || strings.TrimSpace(b.Prompt) == "" || len(b.NudgePreset) > 12 {
		fail(w, 400, "invalid", "stale_after_s >= 60, handoff_timeout_s 10..1800, a handoff_prompt and at most 12 nudge_presets are required.")
		return
	}
	write(w, 200, b)
}

func boolOK(v any) bool { b, _ := v.(bool); return b }

type fixtureEvent struct {
	ID   *int64         `json:"id"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// serveStream replays fixtures/events.json as SSE. Frames are `data: {json}`,
// with `id:` on replayable types; Last-Event-ID (or ?last_event_id) skips
// everything up to and including that id. ?once=1 ends the stream after the
// replay; otherwise it holds open with 15 s keepalive comments like the real one.
func serveStream(w http.ResponseWriter, r *http.Request) {
	var evs []fixtureEvent
	if !load("events", &evs) {
		fail(w, 500, "internal", "bad events fixture")
		return
	}
	var last int64
	if v := r.Header.Get("Last-Event-ID"); v != "" || r.URL.Query().Get("last_event_id") != "" {
		if v == "" {
			v = r.URL.Query().Get("last_event_id")
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(w, 400, "invalid", "Last-Event-ID must be a non-negative integer.")
			return
		}
		last = n
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "internal", "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Tachyon-Agents-Stub", "1")
	w.WriteHeader(200)
	base := time.Now().UnixMilli()
	for i, e := range evs {
		if e.ID != nil && *e.ID <= last {
			continue
		}
		if e.ID == nil && last > 0 {
			continue // live snapshots are not replayed on resume; the client refetches
		}
		env := map[string]any{"type": e.Type, "at": base + int64(i)}
		for k, v := range e.Data {
			env[k] = v
		}
		body, _ := json.Marshal(env)
		if e.ID != nil {
			fmt.Fprintf(w, "id: %d\n", *e.ID)
		}
		fmt.Fprintf(w, "data: %s\n\n", body)
	}
	fl.Flush()
	if r.URL.Query().Get("once") == "1" {
		return
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			fmt.Fprint(w, ": keepalive\n\n")
			fl.Flush()
		}
	}
}
