package builderapi

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed fixtures/*.json openapi.yaml
var assets embed.FS

// OpenAPI is the contract document, served at GET <Prefix>/openapi.yaml by the stub.
func OpenAPI() []byte { b, _ := assets.ReadFile("openapi.yaml"); return b }

// Stub serves the fixtures under Prefix and RegistryPrefix. Handlers are mounted
// by pattern, so an unknown path is a 404 here and never falls through to the
// unguarded /api/plugins/ catch-all. Writes validate their bodies like the real
// routes will, but change nothing.
type Stub struct{}

func (Stub) Handler() http.Handler {
	m := http.NewServeMux()
	wrap := func(f http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Tachyon-Builder-Stub", "1")
			// Error-shape tests: X-Stub-Status: 400|403|404|409|413|429|503.
			if s, _ := strconv.Atoi(r.Header.Get("X-Stub-Status")); s >= 400 && s < 600 {
				fail(w, s, errCode(s), "Forced by X-Stub-Status.")
				return
			}
			f(w, r)
		}
	}
	b := func(pattern string, f http.HandlerFunc) {
		method, path, _ := strings.Cut(pattern, " ")
		m.HandleFunc(method+" "+Prefix+path, wrap(f))
	}
	b("GET /summary", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Stub-Scenario") == "catalog_down" {
			fixture(w, 200, "summary_catalog_down")
			return
		}
		fixture(w, 200, "summary")
	})
	b("GET /agents", agentsList)
	b("GET /agents/{id}", func(w http.ResponseWriter, r *http.Request) { byID(w, r, "agent_details", "agent") })
	b("GET /agents/{id}/resolved", func(w http.ResponseWriter, r *http.Request) { catalogOnly(w, r, "agent_resolved") })
	b("GET /agents/{id}/used_by", func(w http.ResponseWriter, r *http.Request) { byID(w, r, "agent_used_by", "agent") })
	b("GET /agents/{id}/checks", func(w http.ResponseWriter, r *http.Request) { catalogOnly(w, r, "agent_checks") })
	b("GET /agents/{id}/history", func(w http.ResponseWriter, r *http.Request) { catalogOnly(w, r, "agent_history") })
	b("GET /launches", launchesList)
	b("GET /launches/{id}", func(w http.ResponseWriter, r *http.Request) {
		if l, ok := findLaunch(w, r); ok {
			write(w, 200, l)
		}
	})
	b("POST /launches/{id}/launch", launchAction)
	b("GET /library/{kind}", libraryList)
	b("GET /library/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) { libraryItem(w, r, "library_detail") })
	b("GET /library/{kind}/{id}/used_by", func(w http.ResponseWriter, r *http.Request) { libraryItem(w, r, "library_used_by") })
	b("GET /library/{kind}/{id}/history", func(w http.ResponseWriter, r *http.Request) { libraryItem(w, r, "library_history") })
	b("GET /changes", changesList)
	b("POST /changes", changeCreate)
	b("GET /changes/{id}", func(w http.ResponseWriter, r *http.Request) {
		if c, ok := findChange(w, r.PathValue("id")); ok {
			write(w, 200, c)
		}
	})
	b("POST /changes/{id}/submit", changeSubmit)
	b("POST /changes/{id}/cancel", changeCancel)
	b("POST /changes/{id}/rearm", changeRearm)
	b("GET /capabilities", capabilities)
	b("GET /changes/{id}/bundle", changeBundle)
	b("POST /changes/{id}/mark_planned", changeMarkPlanned)
	b("POST /changes/{id}/check", changeCheck)
	m.HandleFunc("GET "+Prefix+"/stream", serveStream)
	m.HandleFunc("GET "+Prefix+"/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(OpenAPI())
	})
	reg := func(pattern string, f http.HandlerFunc) {
		method, path, _ := strings.Cut(pattern, " ")
		m.HandleFunc(method+" "+RegistryPrefix+path, wrap(f))
	}
	reg("GET /projects", projectsList)
	reg("GET /projects/{id}", func(w http.ResponseWriter, r *http.Request) { byID(w, r, "registry_project_details", "project") })
	reg("PATCH /projects/{id}", projectPatch)
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
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 413:
		return "too_large"
	case 429:
		return "rate_limited"
	case 503:
		return "not_configured"
	}
	return "internal"
}

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 512<<10))
	if err != nil {
		fail(w, 400, "invalid", "Body must be a JSON object.")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, "invalid", "Send a JSON object with the documented fields only.")
		return false
	}
	return true
}

// byID serves one entry of a fixture map keyed by id.
func byID(w http.ResponseWriter, r *http.Request, name, what string) {
	var m map[string]json.RawMessage
	load(name, &m)
	if v, ok := m[r.PathValue("id")]; ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(v)
		return
	}
	fail(w, 404, "not_found", "No such "+what+".")
}

// catalogOnly is byID for routes that exist only for catalog agents: an owned
// (Tachyon-side) draft has no file, so there is nothing to resolve, check or version.
func catalogOnly(w http.ResponseWriter, r *http.Request, name string) {
	var det map[string]map[string]any
	load("agent_details", &det)
	if d, ok := det[r.PathValue("id")]; ok && d["owned_draft"] == true {
		fail(w, 404, "not_catalog_agent", "This is a Tachyon-owned draft, not a catalog agent.")
		return
	}
	byID(w, r, name, "agent")
}

func agentsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := "agents"
	switch q.Get("cursor") {
	case "":
	case "c2":
		page = "agents_page2"
	default:
		fail(w, 400, "invalid", "Unknown cursor.")
		return
	}
	state := q.Get("state")
	switch state {
	case "", AgentActive, AgentDisabled, AgentRetired:
	default:
		fail(w, 400, "invalid", "state must be active, disabled or retired.")
		return
	}
	var d struct {
		Agents     []map[string]any `json:"agents"`
		Total      int              `json:"total"`
		NextCursor *string          `json:"next_cursor"`
	}
	load(page, &d)
	text, prov := strings.ToLower(q.Get("q")), q.Get("provider")
	out := d.Agents[:0:0]
	for _, a := range d.Agents {
		if state != "" && a["state"] != state || prov != "" && a["provider"] != prov ||
			text != "" && !strings.Contains(strings.ToLower(fmt.Sprint(a["id"], " ", a["name"])), text) {
			continue
		}
		out = append(out, a)
	}
	d.Agents = out // total stays global: it counts every agent
	write(w, 200, d)
}

func launchList() []map[string]any {
	var d struct {
		Launches []map[string]any `json:"launches"`
	}
	load("launches", &d)
	return d.Launches
}

func launchesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := strings.ToLower(q.Get("q"))
	out := []map[string]any{}
	for _, l := range launchList() {
		if v := q.Get("provider"); v != "" && l["provider"] != v || q.Get("project") != "" && l["project"] != q.Get("project") ||
			q.Get("worker") != "" && l["worker"] != q.Get("worker") {
			continue
		}
		if q.Get("live") == "1" && l["live_count"].(float64) == 0 {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(fmt.Sprint(l["id"], " ", l["agent"])), text) {
			continue
		}
		out = append(out, l)
	}
	write(w, 200, map[string]any{"launches": out, "next_cursor": nil})
}

func findLaunch(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	for _, l := range launchList() {
		if l["id"] == r.PathValue("id") {
			return l, true
		}
	}
	fail(w, 404, "not_found", "No such launch.")
	return nil, false
}

// launchAction starts a session from a catalog launch. It accepts only
// request_id: no environment or override field ever comes from the UI.
func launchAction(w http.ResponseWriter, r *http.Request) {
	l, ok := findLaunch(w, r)
	if !ok {
		return
	}
	var b struct {
		RequestID string `json:"request_id"`
	}
	if !readBody(w, r, &b) {
		return
	}
	if b.RequestID == "" {
		fail(w, 400, "invalid", "request_id is required.")
		return
	}
	switch {
	case l["enabled"] == false:
		fail(w, 409, "agent_disabled", "This launch is disabled, so it cannot start new sessions.")
	case r.Header.Get("X-Stub-Scenario") == "worker_unavailable":
		fail(w, 409, "worker_unavailable", "The worker for this launch is offline.")
	default:
		fixture(w, 201, "launch_started")
	}
}

func validKind(w http.ResponseWriter, r *http.Request) (string, bool) {
	k := r.PathValue("kind")
	for _, v := range LibraryKinds {
		if k == v {
			return k, true
		}
	}
	fail(w, 400, "invalid", "kind must be prompts, skills, tool_servers or policies.")
	return "", false
}

func libraryList(w http.ResponseWriter, r *http.Request) {
	k, ok := validKind(w, r)
	if !ok {
		return
	}
	var d struct {
		Items []map[string]any `json:"items"`
	}
	load("library_"+k, &d)
	text := strings.ToLower(r.URL.Query().Get("q"))
	out := d.Items[:0:0]
	for _, it := range d.Items {
		if text != "" && !strings.Contains(strings.ToLower(fmt.Sprint(it["id"], " ", it["name"], " ", it["summary"])), text) {
			continue
		}
		out = append(out, it)
	}
	write(w, 200, map[string]any{"items": out})
}

func libraryItem(w http.ResponseWriter, r *http.Request, name string) {
	k, ok := validKind(w, r)
	if !ok {
		return
	}
	var m map[string]json.RawMessage
	load(name, &m)
	if v, ok := m[k+"/"+r.PathValue("id")]; ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(v)
		return
	}
	fail(w, 404, "not_found", "No such library item.")
}

func projectsList(w http.ResponseWriter, r *http.Request) {
	var d struct {
		Projects   []map[string]any `json:"projects"`
		NextCursor *string          `json:"next_cursor"`
	}
	load("registry_projects", &d)
	text := strings.ToLower(r.URL.Query().Get("q"))
	out := d.Projects[:0:0]
	for _, p := range d.Projects {
		if text != "" && !strings.Contains(strings.ToLower(fmt.Sprint(p["id"], " ", p["display_name"])), text) {
			continue
		}
		out = append(out, p)
	}
	d.Projects = out
	write(w, 200, d)
}

// projectPatch applies the registry write allowlist: display_name, description,
// tags, guidelines, entry_points, status. Unknown fields and null are rejected.
// catalog.mcp_servers is not writable here; it goes through POST /changes.
func projectPatch(w http.ResponseWriter, r *http.Request) {
	var det map[string]map[string]any
	load("registry_project_details", &det)
	p, ok := det[r.PathValue("id")]
	if !ok {
		fail(w, 404, "not_found", "No such project.")
		return
	}
	var in map[string]any
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || json.Unmarshal(b, &in) != nil || len(in) == 0 {
		fail(w, 400, "invalid", "Body must be a JSON object with at least one field.")
		return
	}
	identity, _ := p["identity"].(map[string]any)
	guidance, _ := p["guidance"].(map[string]any)
	for k, v := range in {
		allowed := false
		for _, f := range ProjectWriteFields {
			allowed = allowed || f == k
		}
		if !allowed || v == nil {
			fail(w, 400, "invalid", "Field "+strconv.Quote(k)+" is not writable (or is null).")
			return
		}
		if !validProjectField(k, v) {
			fail(w, 400, "invalid", "Field "+strconv.Quote(k)+" has the wrong type or size.")
			return
		}
		switch k {
		case "guidelines", "entry_points":
			guidance[k] = v
		default:
			identity[k] = v
		}
	}
	write(w, 200, p)
}

func validProjectField(k string, v any) bool {
	str := func(max int) bool { s, ok := v.(string); return ok && utf8.RuneCountInString(s) <= max }
	list := func(n, max int) bool {
		a, ok := v.([]any)
		if !ok || len(a) > n {
			return false
		}
		for _, e := range a {
			if s, ok := e.(string); !ok || s == "" || utf8.RuneCountInString(s) > max {
				return false
			}
		}
		return true
	}
	switch k {
	case "display_name":
		s, _ := v.(string)
		return str(120) && strings.TrimSpace(s) != ""
	case "description":
		return str(2000)
	case "guidelines":
		return str(8000)
	case "tags":
		return list(20, 40)
	case "entry_points":
		return list(20, 200)
	case "status":
		s, _ := v.(string)
		return s == "active" || s == "paused" || s == "archived"
	}
	return false
}

func changeMap() map[string]map[string]any {
	m := map[string]map[string]any{}
	load("changes", &m)
	return m
}

func findChange(w http.ResponseWriter, id string) (map[string]any, bool) {
	if c, ok := changeMap()[id]; ok {
		return c, true
	}
	fail(w, 404, "not_found", "No such change.")
	return nil, false
}

// as returns a fixture change re-labelled with the id the caller asked about,
// so the stub answers a submit or cancel for any fixture id with a coherent object.
func as(c map[string]any, id string) map[string]any {
	out := map[string]any{}
	for k, v := range c {
		out[k] = v
	}
	out["change_id"] = id
	return out
}

func changesList(w http.ResponseWriter, r *http.Request) {
	st := r.URL.Query().Get("state")
	if st != "" && st != "all" && st != "open" {
		valid := false
		for _, s := range ChangeStates {
			valid = valid || s == st
		}
		if !valid {
			fail(w, 400, "invalid", "state must be open, all or one change state.")
			return
		}
	}
	all := changeMap()
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := []map[string]any{}
	for _, id := range ids {
		s, _ := all[id]["state"].(string)
		if st == "open" && !Open(s) || st != "" && st != "all" && st != "open" && s != st {
			continue
		}
		out = append(out, all[id])
	}
	write(w, 200, map[string]any{"changes": out})
}

type changeOp struct {
	Target struct {
		Kind     string `json:"kind"`
		ID       string `json:"id"`
		PartKind string `json:"part_kind"`
	} `json:"target"`
	ExpectedSHA string `json:"expected_sha"`
	Op          struct {
		Type        string          `json:"type"`
		Path        string          `json:"path"`
		Value       json.RawMessage `json:"value"`
		SnapshotSeq int64           `json:"snapshot_seq"`
	} `json:"op"`
}

// changeCreate validates a change request the way the real route will. It
// stores nothing: the answer is chg_draft, or chg_draft_privileged when an op
// touches agent authority. X-Stub-Scenario: stale_base | change_open | rate_limited. In handoff mode
// (the default) an unprivileged change is awaiting_handoff at once; X-Stub-Scenario direct plays the original flow.
func changeCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestID string     `json:"request_id"`
		Note      string     `json:"note"`
		Ops       []changeOp `json:"ops"`
	}
	if !readBody(w, r, &in) {
		return
	}
	if in.RequestID == "" || len(in.RequestID) > 128 || len(in.Ops) == 0 || len(in.Ops) > MaxOps || utf8.RuneCountInString(in.Note) > MaxNoteChars {
		fail(w, 400, "invalid", "request_id, 1 to 20 ops and a note of at most 500 characters are required.")
		return
	}
	privileged := false
	for i, o := range in.Ops {
		if code, msg := checkOp(o); code != "" {
			status := 400
			if code == "too_large" {
				status = 413
			}
			fail(w, status, code, fmt.Sprintf("ops[%d]: %s", i, msg))
			return
		}
		if o.Op.Type == OpArchive {
			var u map[string]struct {
				Counts map[string]int `json:"counts"`
			}
			load("library_used_by", &u)
			if n := u[o.Target.PartKind+"s/"+o.Target.ID].Counts["agents"]; n > 0 {
				fail(w, 409, "part_in_use", fmt.Sprintf("ops[%d]: %d agent(s) still reference this part. Remove the references first.", i, n))
				return
			}
		}
		if o.Op.Type == OpCreate && exists(o) {
			fail(w, 409, "exists", fmt.Sprintf("ops[%d]: %s %q already exists.", i, o.Target.Kind, o.Target.ID))
			return
		}
		privileged = privileged || PrivilegedPath(o.Op.Path) || o.Target.Kind == TargetPart && o.Target.PartKind == "policy"
	}
	switch r.Header.Get("X-Stub-Scenario") {
	case "stale_base":
		fail(w, 409, "stale_base", "expected_sha is no longer the file's sha. Reload to see the change.")
		return
	case "change_open":
		fail(w, 409, "change_open", "Another change is open on this target (chg_awaiting).")
		return
	case "rate_limited":
		fail(w, 429, "rate_limited", "At most 10 changes per minute.")
		return
	}
	id := "chg_draft"
	switch {
	case privileged:
		id = "chg_draft_privileged"
	case stubMode(r) == ModeHandoff: // no ack needed, so the bundle is ready at once
		id = "chg_awaiting_handoff"
	}
	c := changeMap()[id]
	c["request_id"], c["note"] = in.RequestID, in.Note
	write(w, 201, c)
}

// exists reports whether a create op names an entity the fixtures already hold.
func exists(o changeOp) bool {
	switch o.Target.Kind {
	case TargetAgent:
		var det map[string]json.RawMessage
		load("agent_details", &det)
		_, ok := det[o.Target.ID]
		return ok
	case TargetLaunch:
		for _, l := range launchList() {
			if l["id"] == o.Target.ID {
				return true
			}
		}
	case TargetSkill:
		var det map[string]json.RawMessage
		load("library_detail", &det)
		_, ok := det["skills/"+o.Target.ID]
		return ok
	}
	return false
}

// checkOp returns an error code and message when the op is malformed.
func checkOp(o changeOp) (string, string) {
	switch o.Target.Kind {
	case TargetAgent, TargetLaunch, TargetProject, TargetSkill:
		if o.Target.PartKind != "" {
			return "invalid", "part_kind belongs to part targets only."
		}
	case TargetPart:
		if o.Target.PartKind != "prompt" && o.Target.PartKind != "policy" {
			return "invalid", "a part target needs part_kind prompt or policy."
		}
	default:
		return "invalid", "target.kind must be agent, launch, project, skill or part."
	}
	if !IDPattern.MatchString(o.Target.ID) {
		return "invalid", "target.id must match ^[a-z0-9][a-z0-9._-]{0,63}$."
	}
	has := len(o.Op.Value) > 0 && string(o.Op.Value) != "null"
	switch o.Op.Type {
	case OpSet, OpAppend, OpRemove:
		if !FieldPath.MatchString(o.Op.Path) || len(o.Op.Path) > 200 {
			return "invalid", "op.path must be a field path such as policy.permission_mode."
		}
		if o.Op.Type != OpRemove && !has {
			return "invalid", "op.value is required."
		}
		if o.ExpectedSHA == "" {
			return "invalid", "expected_sha is required."
		}
	case OpCreate:
		if !has {
			return "invalid", "op.value is required."
		}
		if o.ExpectedSHA != "" {
			return "invalid", "expected_sha must be empty when creating."
		}
	case OpRestoreVersion:
		if o.Op.SnapshotSeq <= 0 || o.ExpectedSHA == "" {
			return "invalid", "restore_version needs op.snapshot_seq and expected_sha."
		}
	case OpArchive:
		if o.Target.Kind != TargetPart {
			return "invalid", "archive applies to Library parts only. Retire an agent by disabling it."
		}
	default:
		return "invalid", "op.type must be set, append, remove, create, restore_version or archive (delete is not offered)."
	}
	if has {
		if strings.Contains(string(o.Op.Value), MarkerOpen) || strings.Contains(string(o.Op.Value), MarkerClose) {
			return "marker_forbidden", "text must not contain a tachyon:prompt marker."
		}
		limit := MaxFileBytes
		if o.Target.Kind == TargetPart && o.Target.PartKind == "prompt" {
			limit = MaxPromptBytes
		}
		if len(o.Op.Value) > limit {
			return "too_large", fmt.Sprintf("value is larger than %d bytes.", limit)
		}
	}
	return "", ""
}

func changeSubmit(w http.ResponseWriter, r *http.Request) {
	c, ok := findChange(w, r.PathValue("id"))
	if !ok {
		return
	}
	var in struct {
		AckPrivileged bool `json:"ack_privileged"`
	}
	if !readBody(w, r, &in) {
		return
	}
	if c["state"] != ChangeDraft {
		fail(w, 409, "bad_state", "Only a draft can be submitted.")
		return
	}
	priv, _ := c["privileged"].([]any)
	if len(priv) > 0 && !in.AckPrivileged {
		fail(w, 400, "ack_required", "This change touches agent authority. Send ack_privileged: true after listing the privileged fields to the operator.")
		return
	}
	next := "chg_awaiting"
	switch {
	case len(priv) > 0:
		next = "chg_awaiting_privileged"
	case stubMode(r) == ModeHandoff:
		next = "chg_awaiting_handoff"
	}
	write(w, 200, as(changeMap()[next], r.PathValue("id")))
}

func changeCancel(w http.ResponseWriter, r *http.Request) {
	c, ok := findChange(w, r.PathValue("id"))
	if !ok {
		return
	}
	switch c["state"] {
	case ChangeDraft, ChangeAwaitingHandoff, ChangeAwaitingApproval, ChangeStale:
		write(w, 200, as(changeMap()["chg_cancelled"], r.PathValue("id")))
	default:
		fail(w, 409, "bad_state", "Only a draft, an awaiting-handoff, an awaiting-approval or a stale change can be cancelled.")
	}
}

// changeRearm asks Tether to re-arm the same plan (same bundle, same plan_id)
// after its 15 minute expiry.
func changeRearm(w http.ResponseWriter, r *http.Request) {
	c, ok := findChange(w, r.PathValue("id"))
	if !ok {
		return
	}
	if c["state"] != ChangeAwaitingApproval && c["state"] != ChangeExpired {
		fail(w, 409, "bad_state", "Only an awaiting-approval or expired change can be re-armed.")
		return
	}
	n := as(changeMap()["chg_awaiting"], r.PathValue("id"))
	plan := map[string]any{}
	for k, v := range n["plan"].(map[string]any) {
		plan[k] = v
	}
	plan["expires_at"] = time.Now().UnixMilli() + PlanTTLSeconds*1000
	n["plan"] = plan
	write(w, 200, n)
}

// handoffCommands is what the server builds so the UI never hard-codes CLI flags. Tether's CLI has show and
// approve only; planning and applying are MCP tools of a session that holds catalog.write (kind action, no
// shell command). The plan id is a placeholder until mark_planned gives it.
func handoffCommands(planID string) []map[string]any {
	if planID == "" {
		planID = "<plan_id>"
	}
	return []map[string]any{
		{"step": "plan", "label": "Ask a session with catalog.write to plan this bundle", "kind": "action", "tool": "tether_catalog_plan"},
		{"step": "show", "label": "Review the plan", "kind": "shell", "command": "tether catalog show " + planID},
		{"step": "approve", "label": "Approve the plan", "kind": "shell", "command": "tether catalog approve " + planID},
		{"step": "apply", "label": "Ask the same session to apply the approved plan", "kind": "action", "tool": "tether_catalog_apply"},
	}
}

// stubMode is the change mode the stub plays: handoff (the real default, no control token) unless
// X-Stub-Scenario is direct or unavailable.
func stubMode(r *http.Request) string {
	switch m := r.Header.Get("X-Stub-Scenario"); m {
	case ModeDirect, ModeUnavailable:
		return m
	}
	return ModeHandoff
}

func capabilities(w http.ResponseWriter, r *http.Request) {
	var all map[string]any
	load("capabilities", &all)
	write(w, 200, all[stubMode(r)])
}

// changeBundle serves the POST /catalog/plans body for an open handoff change. Real bundles carry full file
// content, so the route is browser-session only and never cached.
func changeBundle(w http.ResponseWriter, r *http.Request) {
	c, ok := findChange(w, r.PathValue("id"))
	if !ok {
		return
	}
	if c["state"] != ChangeAwaitingHandoff && c["state"] != ChangeAwaitingApproval || c["handoff"] == nil {
		fail(w, 409, "bad_state", "Only a change waiting on a Tether hand-off has a bundle.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var v map[string]any
	load("change_bundle", &v)
	v["name"] = "builder " + r.PathValue("id")
	v["commands"] = handoffCommands("")
	write(w, 200, v)
}

// changeMarkPlanned records the plan id the operator got from Tether, only as a hint for the UI.
func changeMarkPlanned(w http.ResponseWriter, r *http.Request) {
	c, ok := findChange(w, r.PathValue("id"))
	if !ok {
		return
	}
	var in struct {
		PlanID string `json:"plan_id"`
	}
	if !readBody(w, r, &in) {
		return
	}
	if len(in.PlanID) > 80 || strings.ContainsAny(in.PlanID, " \t\n/") {
		fail(w, 400, "invalid", "plan_id is a Tether plan id of at most 80 characters.")
		return
	}
	if c["state"] != ChangeAwaitingHandoff && c["state"] != ChangeAwaitingApproval || c["handoff"] == nil {
		fail(w, 409, "bad_state", "Only a change waiting on a Tether hand-off can be marked planned.")
		return
	}
	n := as(changeMap()["chg_handoff_planned"], r.PathValue("id"))
	if in.PlanID != "" {
		plan := map[string]any{}
		for k, v := range n["plan"].(map[string]any) {
			plan[k] = v
		}
		plan["plan_id"], plan["approve_command"] = in.PlanID, "tether catalog approve "+in.PlanID
		n["plan"] = plan
		if h, ok := n["handoff"].(map[string]any); ok {
			hh := map[string]any{}
			for k, v := range h {
				hh[k] = v
			}
			hh["commands"] = handoffCommands(in.PlanID)
			n["handoff"] = hh
		}
	}
	write(w, 200, n)
}

// changeCheck is "Check now": the server re-reads the files and answers the change. X-Stub-Scenario detected
// answers applied, stale answers stale, anything else the change as it is.
func changeCheck(w http.ResponseWriter, r *http.Request) {
	c, ok := findChange(w, r.PathValue("id"))
	if !ok {
		return
	}
	if c["state"] != ChangeAwaitingHandoff && c["state"] != ChangeAwaitingApproval || c["handoff"] == nil {
		write(w, 200, c)
		return
	}
	switch r.Header.Get("X-Stub-Scenario") {
	case "detected":
		c = as(changeMap()["chg_applied_detected"], r.PathValue("id"))
	case "stale":
		c = as(changeMap()["chg_stale"], r.PathValue("id"))
	}
	write(w, 200, c)
}

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
	h.Set("X-Tachyon-Builder-Stub", "1")
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
