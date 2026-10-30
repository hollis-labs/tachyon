package transcriptsapi

import (
	"embed"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

//go:embed fixtures/*.json openapi.yaml
var assets embed.FS

// Stub serves the fixtures. Read-only; there is no state.
//
// Controls: X-Stub-Status forces a status with an error body. X-Stub-Scenario on
// /snapshot: cached, stale, unavailable, partial, empty (default live); on /search:
// semantic_unavailable (text results plus a notice), empty; on /status: degraded.
type Stub struct{}

func (Stub) Handler() http.Handler {
	m := http.NewServeMux()
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		m.HandleFunc(method+" "+Prefix+path, wrap(handlerFor(path)))
	}
	return m
}

func errBody(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

func wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Tachyon-Transcripts-Stub", "1")
		if r.Header.Get("Authorization") != "" {
			errBody(w, 403, "forbidden", "Transcripts are available to the operator's browser session only.")
			return
		}
		if v := r.Header.Get("X-Stub-Status"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 400 || n > 599 {
				errBody(w, 400, "invalid_args", "X-Stub-Status must be 400-599.")
				return
			}
			errBody(w, n, map[int]string{401: "authentication", 403: "authentication", 404: "not_found", 429: "rate_limited", 503: "configuration"}[n], "Forced by X-Stub-Status.")
			return
		}
		h(w, r)
	}
}

func fixture(name string) []byte {
	b, err := assets.ReadFile("fixtures/" + name + ".json")
	if err != nil {
		panic(err)
	}
	return b
}

func serve(w http.ResponseWriter, name string) { _, _ = w.Write(fixture(name)) }

func handlerFor(path string) http.HandlerFunc {
	switch path {
	case "/snapshot":
		return snapshot
	case "/status":
		return func(w http.ResponseWriter, r *http.Request) {
			if !only(w, r) {
				return
			}
			if r.Header.Get("X-Stub-Scenario") == "degraded" {
				serve(w, "status_degraded")
				return
			}
			serve(w, "status")
		}
	case "/search":
		return search
	case "/stats":
		return func(w http.ResponseWriter, r *http.Request) {
			if only(w, r, filterKeys()...) && filters(w, r) {
				serve(w, "stats")
			}
		}
	case "/openapi.yaml":
		return func(w http.ResponseWriter, r *http.Request) {
			b, _ := assets.ReadFile("openapi.yaml")
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(b)
		}
	case "/sessions/{id}":
		return detail
	case "/sessions/{id}/find":
		return find
	default:
		return errorsList
	}
}

func filterKeys() []string { return append([]string(nil), Filters...) }

// only rejects unknown query parameters (the allow-list rule).
func only(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for k := range r.URL.Query() {
		ok := false
		for _, a := range allowed {
			ok = ok || a == k
		}
		if !ok {
			errBody(w, 400, "invalid_args", "Unknown parameter "+strconv.Quote(k)+".")
			return false
		}
	}
	return true
}

func in(s string, set []string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

func filters(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	if v := q.Get("has_error"); v != "" && v != "true" && v != "false" {
		errBody(w, 400, "invalid_args", "has_error must be true or false.")
		return false
	}
	for _, k := range Filters {
		if len(q.Get(k)) > 128 {
			errBody(w, 400, "invalid_args", k+" is too long.")
			return false
		}
	}
	return true
}

func intParam(w http.ResponseWriter, r *http.Request, name string, def, lo, hi int) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		errBody(w, 400, "invalid_args", "Invalid "+name+".")
		return 0, false
	}
	return n, true
}

func snapshot(w http.ResponseWriter, r *http.Request) {
	if !only(w, r) {
		return
	}
	name := map[string]string{"": "snapshot_live", "live": "snapshot_live", "cached": "snapshot_cached", "stale": "snapshot_stale",
		"unavailable": "snapshot_unavailable", "partial": "snapshot_partial", "empty": "snapshot_empty"}[r.Header.Get("X-Stub-Scenario")]
	if name == "" {
		errBody(w, 400, "invalid_args", "Unknown snapshot scenario.")
		return
	}
	// The real bridge also answers 200 for the unavailable state, with the saved snapshot.
	serve(w, name)
}

func search(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, append(filterKeys(), "q", "mode", "limit", "cursor")...) || !filters(w, r) {
		return
	}
	q := r.URL.Query()
	if q.Get("q") == "" || len(q.Get("q")) > MaxQuery {
		errBody(w, 400, "invalid_args", "q is required and at most 256 characters.")
		return
	}
	mode := q.Get("mode")
	if mode == "" {
		mode = "text"
	}
	if !in(mode, Modes) {
		errBody(w, 400, "invalid_args", "Unknown mode.")
		return
	}
	if _, ok := intParam(w, r, "limit", 20, 1, MaxSearchLimit); !ok {
		return
	}
	if c := q.Get("cursor"); c != "" && c != "c2" {
		errBody(w, 400, "invalid_args", "Invalid cursor.")
		return
	}
	switch sc := r.Header.Get("X-Stub-Scenario"); {
	case sc == "empty":
		serve(w, "search_empty")
	case sc == "semantic_unavailable" || (mode == "semantic" && sc == "degraded"):
		serve(w, "search_semantic_unavailable")
	case sc == "ranges" && mode == "text":
		serve(w, "search_text_ranges")
	case q.Get("cursor") == "c2":
		serve(w, "search_empty")
	default:
		serve(w, "search_"+mode)
	}
}

var details = map[string]string{SessionClaude: "session_a", SessionCodex: "session_b", SessionChatGPT: "session_c"}

func detail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !ValidSessionID(id) {
		errBody(w, 404, "not_found", "Unknown transcript read.")
		return
	}
	if !only(w, r, "turn_offset", "turn_limit", "turn_from_seq") {
		return
	}
	off, ok := intParam(w, r, "turn_offset", 0, 0, MaxTurnOffset)
	if !ok {
		return
	}
	lim, ok := intParam(w, r, "turn_limit", DefaultTurns, 1, MaxTurns)
	if !ok {
		return
	}
	from, ok := intParam(w, r, "turn_from_seq", -1, 0, MaxTurnOffset)
	if !ok {
		return
	}
	name, known := details[id]
	if !known {
		if id == SessionUndated {
			name = "session_c" // summary-only session reuses the small export body
		} else {
			errBody(w, 404, "not_found", "The transcript or transcript API is unavailable.")
			return
		}
	}
	var d map[string]json.RawMessage
	_ = json.Unmarshal(fixture(name), &d)
	var turns []json.RawMessage
	_ = json.Unmarshal(d["turns"], &turns)
	if from >= 0 {
		off = from
	}
	if off > len(turns) {
		off = len(turns)
	}
	end := min(off+lim, len(turns))
	page, _ := json.Marshal(turns[off:end])
	d["turns"] = page
	next := json.RawMessage("null")
	if end < len(turns) {
		next = json.RawMessage(strconv.Itoa(end))
	}
	d["next_turn"] = next
	d["turn_offset"] = json.RawMessage(strconv.Itoa(off))
	_ = json.NewEncoder(w).Encode(d)
}

func known(w http.ResponseWriter, r *http.Request) bool {
	id := r.PathValue("id")
	if !ValidSessionID(id) || details[id] == "" {
		errBody(w, 404, "not_found", "The transcript or transcript API is unavailable.")
		return false
	}
	return true
}

func find(w http.ResponseWriter, r *http.Request) {
	if !known(w, r) || !only(w, r, "q", "after_offset", "limit") {
		return
	}
	if q := r.URL.Query().Get("q"); q == "" || len(q) > MaxQuery {
		errBody(w, 400, "invalid_args", "q is required and at most 256 characters.")
		return
	}
	if _, ok := intParam(w, r, "after_offset", 0, 0, MaxTurnOffset); !ok {
		return
	}
	if _, ok := intParam(w, r, "limit", 20, 1, MaxSearchLimit); !ok {
		return
	}
	if r.PathValue("id") != SessionClaude {
		_, _ = w.Write([]byte(`{"total":0,"matches":[],"truncated":false}`))
		return
	}
	serve(w, "find")
}

func errorsList(w http.ResponseWriter, r *http.Request) {
	if !known(w, r) || !only(w, r) {
		return
	}
	if r.PathValue("id") != SessionClaude {
		_, _ = w.Write([]byte(`{"total":0,"items":[]}`))
		return
	}
	serve(w, "errors")
}
