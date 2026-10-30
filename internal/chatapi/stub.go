package chatapi

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed fixtures/*.json openapi.yaml
var assets embed.FS

// OpenAPI is the contract document, served at GET <Prefix>/openapi.yaml by the stub.
func OpenAPI() []byte { b, _ := assets.ReadFile("openapi.yaml"); return b }

type route struct {
	pattern string // ServeMux pattern relative to the family prefix
	status  int
	fixture string // file in fixtures/, empty for the special handlers
}

// chatRoutes and terminalRoutes are the full route list from the design
// (section 10). Every route answers with the final response shape.
var chatRoutes = []route{
	{"GET /threads", 200, "threads"},
	{"POST /threads", 201, "thread_created"},
	{"GET /threads/{ref}", 200, "thread"},
	{"PATCH /threads/{ref}", 200, "thread"},
	{"GET /threads/{ref}/messages", 200, "messages"},
	{"POST /threads/{ref}/messages", 202, "message_accepted"},
	{"POST /threads/{ref}/read", 200, "ok"},
	{"GET /threads/{ref}/plan", 200, "plan"},
	{"PUT /threads/{ref}/plan", 200, "plan"},
	{"POST /threads/{ref}/plan/steps", 201, "plan_step"},
	{"PATCH /threads/{ref}/plan/steps/{id}", 200, "plan_step"},
	{"POST /threads/{ref}/plan/reorder", 200, "plan"},
	{"GET /threads/{ref}/topics", 200, "topics"},
	{"POST /threads/{ref}/topics", 201, "topic"},
	{"DELETE /threads/{ref}/topics/{id}", 200, "ok"},
	{"GET /threads/{ref}/bookmarks", 200, "bookmarks"},
	{"POST /threads/{ref}/bookmarks", 201, "bookmark"},
	{"DELETE /threads/{ref}/bookmarks/{id}", 200, "ok"},
	{"GET /threads/{ref}/workspace", 200, "workspace"},
	{"POST /threads/{ref}/workspace", 201, "workspace_item"},
	{"PATCH /threads/{ref}/workspace/{id}", 200, "workspace_item"},
	{"DELETE /threads/{ref}/workspace/{id}", 200, "ok"},
	{"GET /threads/{ref}/scratchpad", 200, "scratchpad"},
	{"PUT /threads/{ref}/scratchpad", 200, "scratchpad"},
	{"GET /threads/{ref}/activity", 200, "activity"},
	{"POST /threads/{ref}/activity/comments", 201, "activity_comment"},
	{"GET /threads/{ref}/cards", 200, "cards"},
	{"GET /threads/{ref}/context", 200, "context"},
	{"GET /threads/{ref}/work", 200, "work"},
	{"GET /threads/{ref}/approval-rules", 200, "approval_rules"},
	{"DELETE /threads/{ref}/approval-rules/{id}", 200, "ok"},
	{"GET /settings", 200, "settings"},
	{"PUT /settings", 200, "settings"},
}

var terminalRoutes = []route{
	{"GET /machines", 200, "machines"},
	{"POST /sessions", 201, "terminal_session"},
	{"DELETE /sessions/{id}", 200, "ok"},
	{"GET /proposals", 200, "proposals"},
	{"POST /proposals/{id}/run", 200, "run"},
	{"POST /proposals/{id}/deny", 200, "ok"},
	{"POST /proposals/{id}/edit", 201, "proposal"},
	{"GET /runs", 200, "runs"},
	{"GET /runs/{run_id}", 200, "run"},
	{"GET /runs/{run_id}/output", 200, "run_output"},
	{"POST /runs/{run_id}/stop", 202, "run"},
	{"PATCH /runs/{run_id}", 200, "run"},
}

// WorkspaceContentPath is the one chat GET that needs operator assurance on
// reads (it returns file bytes). The server mounts it explicitly.
const WorkspaceContentPath = Prefix + "/threads/{ref}/workspace/{id}/content"

// Stub serves the fixtures. Handlers are mounted by pattern, not by prefix
// alone, so an unknown path is a 404 here and never falls through to the
// unguarded /api/plugins/ catch-all.
type Stub struct{}

// Chat serves everything under Prefix except the workspace content route.
func (Stub) Chat() http.Handler {
	return mount(Prefix, chatRoutes, func(m *http.ServeMux) {
		m.HandleFunc("GET "+Prefix+"/events", serveEvents(false))
		m.HandleFunc("GET "+Prefix+"/threads/{ref}/events", serveEvents(true))
		m.HandleFunc("GET "+Prefix+"/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(OpenAPI())
		})
	})
}

// WorkspaceContent serves the stub bytes for a workspace item.
func (Stub) WorkspaceContent() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validRef(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="stub.txt"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte("stub workspace content\n"))
	})
}

// Terminal serves everything under TerminalPrefix. The websocket route answers
// 501: it is not stubbable over plain HTTP, and its frames are in openapi.yaml.
func (Stub) Terminal() http.Handler {
	return mount(TerminalPrefix, terminalRoutes, func(m *http.ServeMux) {
		m.HandleFunc("GET "+TerminalPrefix+"/sessions/{id}/ws", func(w http.ResponseWriter, _ *http.Request) {
			fail(w, 501, "stub_unsupported", "The terminal websocket is not stubbed; see openapi.yaml for the frame protocol.")
		})
	})
}

func mount(prefix string, routes []route, extra func(*http.ServeMux)) http.Handler {
	m := http.NewServeMux()
	for _, rt := range routes {
		method, path, _ := strings.Cut(rt.pattern, " ")
		rt := rt
		m.HandleFunc(method+" "+prefix+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Tachyon-Chat-Stub", "1")
			if strings.Contains(rt.pattern, "{ref}") && !validRef(w, r) {
				return
			}
			// Contract tests for error shapes: X-Stub-Status: 400|404|409|429|503.
			if s, _ := strconv.Atoi(r.Header.Get("X-Stub-Status")); s >= 400 && s < 600 {
				fail(w, s, errCode(s), "Forced by X-Stub-Status.")
				return
			}
			b, err := assets.ReadFile("fixtures/" + rt.fixture + ".json")
			if err != nil {
				fail(w, 500, "internal", "missing fixture "+rt.fixture)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(rt.status)
			_, _ = w.Write(b)
		})
	}
	extra(m)
	return m
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

func validRef(w http.ResponseWriter, r *http.Request) bool {
	if _, err := ParseRef(r.PathValue("ref")); err != nil {
		fail(w, 400, "invalid", err.Error())
		return false
	}
	return true
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

type fixtureEvent struct {
	ID   *int64         `json:"id"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// serveEvents replays fixtures/events.json as SSE. Frames are `data: {json}`,
// with `id:` on activity-class events; Last-Event-ID (or ?last_event_id) skips
// everything up to and including that id. ?once=1 ends the stream after the
// replay; otherwise it holds open with 15 s keepalive comments like the real one.
func serveEvents(perThread bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref := "app:t_01"
		if perThread {
			if !validRef(w, r) {
				return
			}
			ref = r.PathValue("ref")
		}
		b, _ := assets.ReadFile("fixtures/events.json")
		var evs []fixtureEvent
		if err := json.Unmarshal(b, &evs); err != nil {
			fail(w, 500, "internal", "bad events fixture")
			return
		}
		var last int64
		if v := firstNonEmpty(r.Header.Get("Last-Event-ID"), r.URL.Query().Get("last_event_id")); v != "" {
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
		h.Set("X-Tachyon-Chat-Stub", "1")
		w.WriteHeader(200)
		base := time.Now().UnixMilli()
		for i, e := range evs {
			if e.ID != nil && *e.ID <= last {
				continue
			}
			if e.ID == nil && last > 0 {
				continue // live-only signals are not replayed on resume
			}
			env := map[string]any{"type": e.Type, "thread_ref": ref, "at": base + int64(i)}
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
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
