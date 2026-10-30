package plannerapi

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

//go:embed fixtures/*.json openapi.yaml
var assets embed.FS

// Stub serves the fixtures. It keeps no state: writes are checked for shape and answer a fixture.
//
// Controls (X-Stub-Scenario): conflict (409 on any write; PUT pin answers revision_conflict with the current item), cycle (422 on POST relations),
// degraded (/momentum), empty (a timeline or the agent work list). X-Stub-Status forces a status
// with an error body.
type Stub struct{}

var (
	taskID    = regexp.MustCompile(`^CW-\d{8}-\d{4}$`)
	fixtureID = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
)

func (Stub) Handler() http.Handler {
	m := http.NewServeMux()
	for _, r := range Routes {
		method, path, _ := strings.Cut(r, " ")
		m.HandleFunc(method+" "+Prefix+path, wrap(handlerFor(method+" "+path)))
	}
	return m
}

func errBody(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}

func wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Tachyon-Planner-Stub", "1")
		if v := r.Header.Get("X-Stub-Status"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 400 || n > 599 {
				errBody(w, 400, "invalid", "X-Stub-Status must be 400-599.")
				return
			}
			errBody(w, n, map[int]string{403: "operator_required", 404: "not_found", 409: "conflict", 502: "source_unavailable"}[n], "Forced by X-Stub-Status.")
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Stub-Scenario") == "conflict" {
			w.WriteHeader(409)
			if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/pin") {
				_, _ = w.Write(fixture("pin_conflict"))
				return
			}
			_, _ = w.Write(fixture("errors_conflict"))
			return
		}
		h(w, r)
	}
}

func raw(name string) []byte {
	b, err := assets.ReadFile("fixtures/" + name + ".json")
	if err != nil {
		panic(err)
	}
	return b
}

func fixture(name string) []byte {
	if name == "errors_conflict" {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw("errors"), &m)
		return m["conflict"]
	}
	return raw(name)
}

func serve(w http.ResponseWriter, name string) { _, _ = w.Write(fixture(name)) }

func in(s string, set []string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

func only(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for k := range r.URL.Query() {
		if !in(k, allowed) {
			errBody(w, 400, "invalid", "Unknown parameter "+strconv.Quote(k)+".")
			return false
		}
	}
	return true
}

// body decodes a JSON object, rejecting unknown fields, trailing data and bodies over the cap.
func body(w http.ResponseWriter, r *http.Request, allowed ...string) (map[string]any, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 3<<20)
	d := json.NewDecoder(r.Body)
	var m map[string]any
	if err := d.Decode(&m); err != nil || m == nil {
		errBody(w, 400, "invalid", "A JSON object body is required.")
		return nil, false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		errBody(w, 400, "invalid", "Unexpected data after the JSON object.")
		return nil, false
	}
	for k := range m {
		if !in(k, allowed) {
			errBody(w, 400, "invalid", "Unknown field "+k+".")
			return nil, false
		}
	}
	return m, true
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func needRevision(w http.ResponseWriter, m map[string]any) bool {
	if n, ok := m["expected_revision"].(float64); !ok || n < 1 || n != float64(int64(n)) {
		errBody(w, 400, "invalid", "expected_revision required.")
		return false
	}
	return true
}

func db(w http.ResponseWriter, r *http.Request) (string, bool) {
	d := r.PathValue("db")
	if !in(d, Databases) {
		errBody(w, 404, "not_found", "Planner item or route not found.")
		return "", false
	}
	return d, true
}

func ref(m any) (string, string, bool) {
	o, ok := m.(map[string]any)
	if !ok {
		return "", "", false
	}
	d, id := str(o, "db"), str(o, "id")
	return d, id, in(d, Databases) && id != ""
}

func handlerFor(route string) http.HandlerFunc {
	switch route {
	case "POST /{db}/{id}/comments":
		return comment
	case "PUT /{db}/{id}/pin":
		return pin
	case "POST /{db}/{id}/relations":
		return relation
	case "DELETE /{db}/{id}/relations":
		return relation
	case "GET /{db}/{id}/links":
		return links
	case "POST /ideas/{id}/promote":
		return promote
	case "GET /themes/{id}/tasks":
		return themeTasks
	case "POST /tasks/{task}/transition":
		return transition
	case "PUT /tasks/{task}/meta":
		return meta
	case "GET /task-context":
		return taskContext
	case "POST /assignments":
		return assignCreate
	case "GET /assignments":
		return assignList
	case "POST /assignments/{id}/complete":
		return assignEnd("assignment_completed")
	case "POST /assignments/{id}/cancel":
		return assignEnd("assignment_cancelled")
	case "GET /agents/{agent_id}/work":
		return agentWork
	case "GET /themes/{id}/timeline", "GET /workstreams/{id}/timeline":
		return timeline
	case "GET /momentum":
		return func(w http.ResponseWriter, r *http.Request) {
			if only(w, r) {
				if r.Header.Get("X-Stub-Scenario") == "degraded" {
					serve(w, "momentum_degraded")
					return
				}
				serve(w, "momentum")
			}
		}
	case "GET /need-you":
		return func(w http.ResponseWriter, r *http.Request) {
			if only(w, r) {
				serve(w, "need_you")
			}
		}
	case "GET /stub/stream":
		return stream
	case "GET /stub/openapi.yaml":
		return func(w http.ResponseWriter, r *http.Request) {
			b, _ := assets.ReadFile("openapi.yaml")
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(b)
		}
	default:
		return fixtureByName
	}
}

// fixtureByName serves the extended shapes of existing routes (list, get, board) and any other fixture.
func fixtureByName(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !fixtureID.MatchString(name) || name == "errors" {
		errBody(w, 404, "not_found", "Planner item or route not found.")
		return
	}
	if _, err := assets.ReadFile("fixtures/" + name + ".json"); err != nil {
		errBody(w, 404, "not_found", "Planner item or route not found.")
		return
	}
	serve(w, name)
}

func comment(w http.ResponseWriter, r *http.Request) {
	d, ok := db(w, r)
	if !ok {
		return
	}
	m, ok := body(w, r, "text", "kind", "status", "pin", "expected_revision")
	if !ok || !needRevision(w, m) {
		return
	}
	text := str(m, "text")
	if strings.TrimSpace(text) == "" || len(text) > MaxCommentBytes {
		errBody(w, 400, "invalid", fmt.Sprintf("text is required and at most %d bytes.", MaxCommentBytes))
		return
	}
	if k := str(m, "kind"); k != "" && k != "comment" && k != "decision" {
		errBody(w, 400, "invalid", "kind must be comment or decision.")
		return
	}
	if _, has := m["pin"]; has {
		if _, isBool := m["pin"].(bool); !isBool {
			errBody(w, 400, "invalid", "pin must be a boolean.")
			return
		}
	}
	if d != "themes" && str(m, "status") != "" && str(m, "status") == "approved" {
		errBody(w, 422, "invalid", "approved is a theme status.")
		return
	}
	serve(w, "comment_status")
}

// pin answers the item with the requested pin state. The stub keeps no state; id "...-unknown" is 404.
func pin(w http.ResponseWriter, r *http.Request) {
	if _, ok := db(w, r); !ok {
		return
	}
	m, ok := body(w, r, "pinned", "expected_revision")
	if !ok || !needRevision(w, m) {
		return
	}
	pinned, isBool := m["pinned"].(bool)
	if !isBool {
		errBody(w, 400, "invalid", "pinned (boolean) is required.")
		return
	}
	if strings.Contains(r.PathValue("id"), "unknown") {
		errBody(w, 404, "not_found", "Planner item or route not found.")
		return
	}
	var v struct {
		Item map[string]any `json:"item"`
	}
	_ = json.Unmarshal(fixture("pin_ok"), &v)
	v.Item["id"], v.Item["db"] = r.PathValue("id"), r.PathValue("db")
	v.Item["pinned"] = pinned
	if !pinned {
		v.Item["pinned_at"] = nil
	}
	_ = json.NewEncoder(w).Encode(v)
}

func relation(w http.ResponseWriter, r *http.Request) {
	from, ok := db(w, r)
	if !ok {
		return
	}
	m, ok := body(w, r, "kind", "to", "expected_revision")
	if !ok || !needRevision(w, m) {
		return
	}
	kind := str(m, "kind")
	toDB, toID, ok := ref(m["to"])
	if !in(kind, RelationKinds) || !ok {
		errBody(w, 400, "invalid", "kind and to{db,id} are required.")
		return
	}
	if toDB == from && toID == r.PathValue("id") {
		errBody(w, 422, "invalid", "An item cannot link to itself.")
		return
	}
	if (kind == RelDelivers && (from != "themes" || toDB != "roadmap")) || (kind == RelMitigates && (from != "themes" || toDB != "risks")) {
		errBody(w, 422, "invalid", kind+" is not allowed between "+from+" and "+toDB+".")
		return
	}
	if kind == RelNeeds && (from != toDB || r.Header.Get("X-Stub-Scenario") == "cycle") {
		if from != toDB {
			errBody(w, 422, "invalid", "needs is within one database.")
			return
		}
		errBody(w, 422, "cycle", "That link would make a cycle.")
		return
	}
	serve(w, "relation_ok")
}

func links(w http.ResponseWriter, r *http.Request) {
	if _, ok := db(w, r); !ok || !only(w, r) {
		return
	}
	if strings.Contains(r.PathValue("id"), "unknown") {
		errBody(w, 404, "not_found", "Planner item or route not found.")
		return
	}
	serve(w, "links")
}

func promote(w http.ResponseWriter, r *http.Request) {
	m, ok := body(w, r, "to_db", "target_id", "fields", "expected_revision")
	if !ok || !needRevision(w, m) {
		return
	}
	if !in(str(m, "to_db"), []string{"themes", "roadmap", "decisions"}) {
		errBody(w, 400, "invalid", "to_db must be themes, roadmap or decisions.")
		return
	}
	serve(w, "promote")
}

func themeTasks(w http.ResponseWriter, r *http.Request) {
	if !only(w, r) {
		return
	}
	if r.PathValue("id") == ThemeUnknown {
		errBody(w, 404, "not_found", "Planner item or route not found.")
		return
	}
	serve(w, "tasks_theme")
}

func transition(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("task")
	if !taskID.MatchString(id) {
		errBody(w, 404, "not_found", "Planner item or route not found.")
		return
	}
	m, ok := body(w, r, "to", "expected_status", "reason", "summary", "pr")
	if !ok {
		return
	}
	to, from := str(m, "to"), str(m, "expected_status")
	if !in(to, Lanes) || !in(from, Lanes) {
		errBody(w, 400, "invalid", "to and expected_status must be one of "+strings.Join(Lanes, ", ")+".")
		return
	}
	if to == "blocked" && strings.TrimSpace(str(m, "reason")) == "" {
		w.WriteHeader(400)
		serve(w, "transition_needs_reason")
		return
	}
	if len(str(m, "summary")) > MaxSummaryChars {
		errBody(w, 400, "invalid", fmt.Sprintf("summary is at most %d characters.", MaxSummaryChars))
		return
	}
	if to == from {
		_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]string{"id": id, "status": to, "previous": to}, "noop": true})
		return
	}
	if id == TaskBlocked && r.Header.Get("X-Stub-Scenario") != "ok" {
		w.WriteHeader(409)
		serve(w, "transition_conflict")
		return
	}
	var v map[string]any
	_ = json.Unmarshal(raw("transition_ok"), &v)
	v["task"] = map[string]string{"id": id, "status": to, "previous": from}
	_ = json.NewEncoder(w).Encode(v)
}

func meta(w http.ResponseWriter, r *http.Request) {
	if !taskID.MatchString(r.PathValue("task")) {
		errBody(w, 404, "not_found", "Planner item or route not found.")
		return
	}
	m, ok := body(w, r, "done_summary", "pr_cites")
	if !ok {
		return
	}
	if s := str(m, "done_summary"); len(s) > MaxSummaryChars {
		errBody(w, 400, "invalid", fmt.Sprintf("done_summary is at most %d characters.", MaxSummaryChars))
		return
	}
	if c, has := m["pr_cites"]; has {
		list, isList := c.([]any)
		if !isList || len(list) > 20 {
			errBody(w, 400, "invalid", "pr_cites must be a list of at most 20.")
			return
		}
		for _, e := range list {
			o, _ := e.(map[string]any)
			if n, _ := o["number"].(float64); str(o, "owner") == "" || str(o, "repo") == "" || n < 1 {
				errBody(w, 400, "invalid", "Each pr cite needs owner, repo and number.")
				return
			}
		}
	}
	serve(w, "meta_ok")
}

func taskContext(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, "task") {
		return
	}
	t := r.URL.Query().Get("task")
	if !taskID.MatchString(t) {
		errBody(w, 400, "invalid", "task must be a Torque task id.")
		return
	}
	if t == "CW-20261030-0999" {
		serve(w, "task_context_no_theme")
		return
	}
	serve(w, "task_context")
}

func assignCreate(w http.ResponseWriter, r *http.Request) {
	m, ok := body(w, r, "target", "agent_id", "team_id", "note")
	if !ok {
		return
	}
	if (str(m, "agent_id") == "") == (str(m, "team_id") == "") {
		errBody(w, 400, "invalid", "Exactly one of agent_id and team_id is required.")
		return
	}
	t, _ := m["target"].(map[string]any)
	_, _, planner := ref(t)
	torque := str(t, "kind") == "torque.task" && taskID.MatchString(str(t, "ref"))
	if planner == torque {
		errBody(w, 400, "invalid", "target must be {db,id} or {kind:torque.task,ref}.")
		return
	}
	if len(str(m, "note")) > 300 {
		errBody(w, 400, "invalid", "note is at most 300 characters.")
		return
	}
	w.WriteHeader(201)
	serve(w, "assignment_created")
}

func assignList(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, "agent_id", "target", "state") {
		return
	}
	q := r.URL.Query()
	if s := q.Get("state"); s != "" && !in(s, AssignmentStates) {
		errBody(w, 400, "invalid", "state must be active, completed or cancelled.")
		return
	}
	var v struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(raw("assignments"), &v)
	out := []map[string]any{}
	for _, it := range v.Items {
		if s := q.Get("state"); s != "" && it["state"] != s {
			continue
		}
		if a := q.Get("agent_id"); a != "" && it["agent_id"] != a {
			continue
		}
		if t := q.Get("target"); t != "" && !targetIs(it["target"], t) {
			continue
		}
		out = append(out, it)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"items": out})
}

// targetIs matches target=<db>/<id> or target=<task id>.
func targetIs(t any, want string) bool {
	o, _ := t.(map[string]any)
	return want == str(o, "ref") || want == str(o, "db")+"/"+str(o, "id")
}

func assignEnd(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case "asg-0001", "asg-0003", "asg-0006":
			serve(w, name)
		case "asg-0004", "asg-0005":
			errBody(w, 409, "conflict", "The assignment already ended.")
		default:
			errBody(w, 404, "not_found", "Planner item or route not found.")
		}
	}
}

func agentWork(w http.ResponseWriter, r *http.Request) {
	if !only(w, r) {
		return
	}
	if r.PathValue("agent_id") == "tachyon_idle" || r.Header.Get("X-Stub-Scenario") == "empty" {
		serve(w, "agent_work_empty")
		return
	}
	serve(w, "agent_work")
}

func timeline(w http.ResponseWriter, r *http.Request) {
	if !only(w, r, "facet", "before_seq", "limit") {
		return
	}
	q := r.URL.Query()
	facet := q.Get("facet")
	if facet == "" {
		facet = "all"
	}
	if !in(facet, Facets) {
		errBody(w, 400, "invalid", "Unknown facet.")
		return
	}
	limit := DefaultTimeline
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxTimeline {
			errBody(w, 400, "invalid", "limit must be 1-100.")
			return
		}
		limit = n
	}
	before := int64(1 << 62)
	if v := q.Get("before_seq"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			errBody(w, 400, "invalid", "before_seq must be a positive integer.")
			return
		}
		before = n
	}
	name := "timeline_rising"
	switch id := r.PathValue("id"); {
	case id == ThemeUnknown:
		errBody(w, 404, "not_found", "Planner item or route not found.")
		return
	case id == ThemeStalled:
		name = "timeline_stalled"
	case strings.HasSuffix(id, "-empty") || r.Header.Get("X-Stub-Scenario") == "empty":
		name = "timeline_empty"
	}
	var v map[string]any
	_ = json.Unmarshal(raw(name), &v)
	kind := map[string]string{"tasks": "task", "prs": "pr", "decisions": "decision", "blockers": "blocker", "comments": "comment", "workstreams": "workstream"}[facet]
	events, _ := v["events"].([]any)
	out := []any{}
	var next any
	for _, e := range events {
		o, _ := e.(map[string]any)
		if seq, _ := o["seq"].(float64); int64(seq) >= before {
			continue
		}
		if kind != "" && o["kind"] != kind {
			continue
		}
		if len(out) == limit {
			last, _ := out[len(out)-1].(map[string]any)
			next = last["seq"]
			break
		}
		out = append(out, e)
	}
	v["events"], v["next_before_seq"] = out, next
	_ = json.NewEncoder(w).Encode(v)
}

// streamScript is the SSE script: ids 101-106 form the replay ring.
var streamScript = []struct {
	id    int
	event string
	data  string
}{
	{101, "planner.changed", `{"refresh":true}`},
	{102, "planner.item", `{"db":"themes","id":"theme-agents-console","rev":15}`},
	{103, "planner.event", `{"theme_id":"theme-agents-console","seq":412}`},
	{104, "planner.task", `{"task":"CW-20261030-0202","status":"review"}`},
	{105, "planner.need_you", `{"total":4}`},
	{106, "planner.changed", `{"refresh":true}`},
}

// stream replays the script after Last-Event-ID (or all of it), sends resync when the id is outside the ring,
// and then ends; a client reconnects as it would after any drop.
func stream(w http.ResponseWriter, r *http.Request) {
	if !only(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	last := 100
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 100 || n > 106 {
			fmt.Fprintf(w, "id: 106\nevent: resync\ndata: {\"reason\":\"beyond_ring\"}\n\n")
			return
		}
		last = n
	}
	for _, e := range streamScript {
		if e.id > last {
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.id, e.event, e.data)
		}
	}
}
