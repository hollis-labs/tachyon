package builderread

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/tachyon/internal/builderapi"
	"github.com/hollis-labs/tachyon/internal/catalogindex"
)

// Reader serves the Builder's GET routes for agents and launches from the catalog index.
type Reader struct {
	Index    *catalogindex.Store
	Sessions SessionSource // nil: live data reports null
	Tools    ToolSource    // nil: tool server health reads "unknown"
}

// Mount registers one explicit pattern per route. wrap guards each (the caller passes operator.RequireOperator).
func (r *Reader) Mount(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	if r.Sessions != nil {
		if _, ok := r.Sessions.(*cached); !ok {
			r.Sessions = newCached(r.Sessions, 5e9, nil)
		}
	}
	p := builderapi.Prefix
	for pat, h := range map[string]http.HandlerFunc{
		"GET " + p + "/agents":                      r.agents,
		"GET " + p + "/agents/{id}":                 r.agent,
		"GET " + p + "/agents/{id}/used_by":         r.usedBy,
		"GET " + p + "/agents/{id}/checks":          r.checks,
		"GET " + p + "/agents/{id}/history":         r.history,
		"GET " + p + "/launches":                    r.launches,
		"GET " + p + "/launches/{id}":               r.launch,
		"GET " + p + "/agents/{id}/resolved":        r.resolved,
		"GET " + p + "/library/{kind}":              r.libraryList,
		"GET " + p + "/library/{kind}/{id}":         r.libraryItem,
		"GET " + p + "/library/{kind}/{id}/used_by": r.libraryUsedBy,
		"GET " + p + "/library/{kind}/{id}/history": r.libraryHistory,
	} {
		mux.Handle(pat, wrap(h))
	}
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	write(w, status, map[string]string{"code": code, "message": msg})
}

func (r *Reader) kind(w http.ResponseWriter, req *http.Request, kind string) ([]file, bool) {
	fs, err := loadAll(req.Context(), r.Index, kind)
	if err != nil {
		fail(w, 503, "catalog_unavailable", "The catalog index could not be read.")
		return nil, false
	}
	return fs, true
}

// page applies the "c<offset>" cursor and limit.
func page[T any](req *http.Request, items []T) ([]T, string) {
	limit := builderapi.PageSize
	if n, err := strconv.Atoi(req.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	off := 0
	if c, ok := strings.CutPrefix(req.URL.Query().Get("cursor"), "c"); ok {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			off = n
		}
	}
	if off > len(items) {
		off = len(items)
	}
	end := off + limit
	if end >= len(items) {
		return items[off:], ""
	}
	return items[off:end], "c" + strconv.Itoa(end)
}

func tag(mode string) string {
	switch mode {
	case "", "default":
		return "ask"
	case "acceptEdits":
		return "edits"
	case "bypassPermissions":
		return "bypass"
	}
	return mode
}

func strs(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return []string{}
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case float64:
		return int(x)
	}
	return 0
}

func orNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *Reader) liveByLaunch(ctx context.Context) (byLaunch, byAgent map[string][]LiveSession, ok bool) {
	if r.Sessions == nil {
		return nil, nil, false
	}
	list, err := r.Sessions.Running(ctx)
	if err != nil {
		return nil, nil, false
	}
	byLaunch, byAgent = map[string][]LiveSession{}, map[string][]LiveSession{}
	for _, s := range list {
		if s.LaunchID != "" {
			byLaunch[s.LaunchID] = append(byLaunch[s.LaunchID], s)
		}
		if s.AgentID != "" {
			byAgent[s.AgentID] = append(byAgent[s.AgentID], s)
		}
	}
	return byLaunch, byAgent, true
}

func (r *Reader) agents(w http.ResponseWriter, req *http.Request) {
	fs, ok := r.kind(w, req, catalogindex.KindAgent)
	if !ok {
		return
	}
	ls, ok := r.kind(w, req, catalogindex.KindLaunch)
	if !ok {
		return
	}
	count := map[string]int{}
	for _, l := range ls {
		count[str(l.Fields["agent"])]++
	}
	q, state := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("q"))), req.URL.Query().Get("state")
	var rows []map[string]any
	for _, f := range fs {
		if state != "" && f.State != state {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(f.ID+" "+f.Name), q) {
			continue
		}
		rows = append(rows, map[string]any{"id": f.ID, "name": orDefault(f.Name, f.ID), "urn": "urn:tether:agent:" + f.ID,
			"provider": orNull(f.Provider), "model": orNull(f.Model), "enabled": f.State == builderapi.AgentActive,
			"state": f.State, "policy_tag": tag(str(f.Fields["permission_mode"])), "launch_count": count[f.ID],
			"layer": f.Layer, "sha": f.SHA, "owned_draft": false})
	}
	out, next := page(req, rows)
	if out == nil {
		out = []map[string]any{}
	}
	write(w, 200, map[string]any{"agents": out, "total": len(rows), "next_cursor": orNull(next)})
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func find(fs []file, id string) (file, bool) {
	for _, f := range fs {
		if f.ID == id {
			return f, true
		}
	}
	return file{}, false
}

// doc parses a file's masked content into a generic map. Parse failure yields an empty map.
func doc(f file) map[string]any {
	m := map[string]any{}
	_ = yaml.Unmarshal([]byte(f.Content), &m)
	return m
}

func sub(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		next, _ := m[k].(map[string]any)
		if next == nil {
			return map[string]any{}
		}
		m = next
	}
	return m
}

func (r *Reader) agent(w http.ResponseWriter, req *http.Request) {
	fs, ok := r.kind(w, req, catalogindex.KindAgent)
	if !ok {
		return
	}
	f, found := find(fs, req.PathValue("id"))
	if !found {
		fail(w, 404, "not_found", "No such agent.")
		return
	}
	ls, ok := r.kind(w, req, catalogindex.KindLaunch)
	if !ok {
		return
	}
	d := doc(f)
	perm, prompt := sub(d, "permissions"), sub(d, "prompt")
	live, _, liveOK := r.liveByLaunch(req.Context())
	launches := []map[string]any{}
	for _, l := range ls {
		if str(l.Fields["agent"]) != f.ID {
			continue
		}
		e := map[string]any{"id": l.ID, "project": str(l.Fields["project"]), "worker": str(l.Fields["worker"]), "enabled": l.State == builderapi.AgentActive, "live_count": nil}
		if liveOK {
			e["live_count"] = len(live[l.ID])
		}
		launches = append(launches, e)
	}
	extends := strs(f.Fields["extends"])
	snaps, _ := loadSnaps(req.Context(), r.Index, f.Path, 1)
	seq := any(nil)
	if len(snaps) > 0 {
		seq = snaps[0].Seq
	}
	write(w, 200, map[string]any{
		"id": f.ID, "state": f.State, "editable": true, "owned_draft": false,
		"identity": map[string]any{"id": f.ID, "name": orDefault(f.Name, f.ID), "description": str(d["description"]),
			"urn": "urn:tether:agent:" + f.ID, "roles": strs(f.Fields["roles"])},
		"runtime": map[string]any{"provider": orNull(f.Provider), "model": orNull(f.Model), "extends": firstOrNull(extends),
			"abstract": f.Fields["abstract"] == true, "enabled": f.State == builderapi.AgentActive},
		"prompt": map[string]any{"system_prompt": str(prompt["system_prompt"]), "agent_prompt": str(prompt["agent_prompt"]),
			"body": firstStr(str(prompt["body"]), str(d["prompt"])), "parts": []any{}, "prompts": []any{}, "boot_fragments": strs(d["boot_fragments"])},
		"tools_skills": map[string]any{"mcp_servers": strs(f.Fields["mcp_servers"]), "skills": strs(f.Fields["skills"])},
		"policy": map[string]any{"permission_mode": str(perm["permission_mode"]), "network": str(perm["network"]),
			"default_sandbox": str(perm["default_sandbox"]), "mcp_scopes": strs(perm["mcp_scopes"]),
			"lifecycle": sub(d, "lifecycle"), "recovery": sub(d, "recovery")},
		"launches": launches,
		"revision": map[string]any{"sha": f.SHA, "snapshot_seq": seq, "layer": f.Layer, "path": f.Path, "observed_at": f.ObservedAt.UnixMilli()},
		"parts":    []any{},
	})
}

func firstOrNull(s []string) any {
	if len(s) == 0 {
		return nil
	}
	return s[0]
}

func firstStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (r *Reader) usedBy(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	fs, ok := r.kind(w, req, catalogindex.KindAgent)
	if !ok {
		return
	}
	if _, found := find(fs, id); !found {
		fail(w, 404, "not_found", "No such agent.")
		return
	}
	ls, ok := r.kind(w, req, catalogindex.KindLaunch)
	if !ok {
		return
	}
	launches := []map[string]any{}
	for _, l := range ls {
		if str(l.Fields["agent"]) == id {
			launches = append(launches, map[string]any{"id": l.ID, "project": str(l.Fields["project"]), "enabled": l.State == builderapi.AgentActive})
		}
	}
	ext := []string{}
	for _, f := range fs {
		for _, e := range strs(f.Fields["extends"]) {
			if e == id {
				ext = append(ext, f.ID)
			}
		}
	}
	sort.Strings(ext)
	out := map[string]any{"launches": launches, "extended_by": ext, "teams": nil, "assigned_items": nil}
	_, byAgent, liveOK := r.liveByLaunch(req.Context())
	counts := map[string]any{"launches": len(launches), "teams": nil, "live_sessions": nil}
	if liveOK {
		sess := []map[string]any{}
		for _, s := range byAgent[id] {
			sess = append(sess, map[string]any{"session_id": s.ID, "launch_id": orNull(s.LaunchID), "state": s.State})
		}
		out["live_sessions"], counts["live_sessions"] = sess, len(sess)
	} else {
		out["live_sessions"] = nil
	}
	out["counts"] = counts
	write(w, 200, out)
}

func (r *Reader) checks(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	fs, ok := r.kind(w, req, catalogindex.KindAgent)
	if !ok {
		return
	}
	f, found := find(fs, id)
	if !found {
		fail(w, 404, "not_found", "No such agent.")
		return
	}
	skills, _ := loadAll(req.Context(), r.Index, catalogindex.KindSkill)
	servers, _ := loadAll(req.Context(), r.Index, catalogindex.KindMCPServer)
	has := func(list []file, id string) bool { _, ok := find(list, id); return ok }
	var missing []string
	for _, s := range strs(f.Fields["skills"]) {
		if !has(skills, s) {
			missing = append(missing, "skill "+s)
		}
	}
	for _, s := range strs(f.Fields["mcp_servers"]) {
		if !has(servers, s) {
			missing = append(missing, "tool server "+s)
		}
	}
	for _, e := range strs(f.Fields["extends"]) {
		if !has(fs, e) {
			missing = append(missing, "parent agent "+e)
		}
	}
	list := []map[string]any{}
	add := func(name, status, msg, remedy string) {
		list = append(list, map[string]any{"name": name, "status": status, "message": msg, "remedy": orNull(remedy), "entity": f.Path})
	}
	if len(missing) == 0 {
		add("references_exist", builderapi.CheckOK, "Skills, tool servers and extends all resolve.", "")
	} else {
		add("references_exist", builderapi.CheckFail, "Not in the catalog: "+strings.Join(missing, ", ")+".", "Create the missing entries or remove the references.")
	}
	if f.Model == "" && f.Fields["abstract"] != true {
		add("model_set", builderapi.CheckWarn, "No model is set and the agent is not abstract.", "Set a model or inherit one with extends.")
	}
	for _, e := range strs(f.Fields["extends"]) {
		if e == f.ID {
			add("extends_cycle", builderapi.CheckFail, "The agent extends itself.", "Remove the self reference.")
		}
	}
	sum := map[string]int{"ok": 0, "warn": 0, "fail": 0}
	for _, c := range list {
		sum[c["status"].(string)]++
	}
	write(w, 200, map[string]any{"checks": list, "summary": sum})
}

func (r *Reader) history(w http.ResponseWriter, req *http.Request) {
	fs, ok := r.kind(w, req, catalogindex.KindAgent)
	if !ok {
		return
	}
	f, found := find(fs, req.PathValue("id"))
	if !found {
		fail(w, 404, "not_found", "No such agent.")
		return
	}
	snaps, err := loadSnaps(req.Context(), r.Index, f.Path, catalogindex.SnapshotsPerPath)
	if err != nil {
		fail(w, 503, "catalog_unavailable", "The catalog index could not be read.")
		return
	}
	entries := make([]map[string]any, 0, len(snaps))
	for i, s := range snaps {
		var diff any
		if i+1 < len(snaps) {
			diff = orNull(unifiedDiff(f.Path, snaps[i+1].Content, s.Content))
		}
		entries = append(entries, map[string]any{"at": s.At.UnixMilli(), "plan_id": orNull(s.PlanID), "name": f.ID + ": " + orDefault(s.Source, "change"),
			"actor": orNull(s.Actor), "files": []string{f.Path}, "privileged": []string{}, "activation": nil, "backup_id": nil,
			"snapshot_seq": s.Seq, "diff": diff})
	}
	out, next := page(req, entries)
	write(w, 200, map[string]any{"entries": out, "next_cursor": orNull(next)})
}

func (r *Reader) launchRow(l file, live map[string][]LiveSession, liveOK bool, agents []file, full bool) map[string]any {
	a, _ := find(agents, str(l.Fields["agent"]))
	keys := strs(l.Fields["env_keys"])
	d := map[string]any{}
	if full {
		d = doc(l)
	}
	ws := map[string]any{}
	for _, k := range []string{"mode", "base"} { // only the shape, never free-form values
		if v := str(sub(d, "workspace")[k]); v != "" {
			ws[k] = v
		}
	}
	row := map[string]any{"id": l.ID, "agent": str(l.Fields["agent"]), "provider": orNull(firstStr(l.Provider, a.Provider)), "model": orNull(firstStr(l.Model, a.Model)),
		"project": str(l.Fields["project"]), "worker": str(l.Fields["worker"]), "workspace": ws,
		"env":   map[string]any{"keys": keys, "secret_like_count": num(l.Fields["secret_like_count"])},
		"route": orNull(str(d["route"])), "chat": d["chat"] == true, "enabled": l.State == builderapi.AgentActive, "live_count": nil, "sha": l.SHA}
	if liveOK {
		row["live_count"] = len(live[l.ID])
	}
	return row
}

func (r *Reader) launches(w http.ResponseWriter, req *http.Request) {
	ls, ok := r.kind(w, req, catalogindex.KindLaunch)
	if !ok {
		return
	}
	as, ok := r.kind(w, req, catalogindex.KindAgent)
	if !ok {
		return
	}
	live, _, liveOK := r.liveByLaunch(req.Context())
	q := req.URL.Query()
	var rows []map[string]any
	for _, l := range ls {
		if (q.Get("agent") != "" && str(l.Fields["agent"]) != q.Get("agent")) || (q.Get("project") != "" && str(l.Fields["project"]) != q.Get("project")) {
			continue
		}
		rows = append(rows, r.launchRow(l, live, liveOK, as, false))
	}
	out, next := page(req, rows)
	if out == nil {
		out = []map[string]any{}
	}
	write(w, 200, map[string]any{"launches": out, "total": len(rows), "next_cursor": orNull(next)})
}

func (r *Reader) launch(w http.ResponseWriter, req *http.Request) {
	ls, ok := r.kind(w, req, catalogindex.KindLaunch)
	if !ok {
		return
	}
	l, found := find(ls, req.PathValue("id"))
	if !found {
		fail(w, 404, "not_found", "No such launch.")
		return
	}
	as, ok := r.kind(w, req, catalogindex.KindAgent)
	if !ok {
		return
	}
	live, _, liveOK := r.liveByLaunch(req.Context())
	write(w, 200, r.launchRow(l, live, liveOK, as, true))
}
