package builderread

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/hollis-labs/tachyon/internal/builderapi"
	"github.com/hollis-labs/tachyon/internal/catalogindex"
)

const previewBytes = 2000

// libKinds maps a Library kind to its catalog kind. Prompts and policies are Tachyon-owned parts (B5) kept in the
// local store; see parts.go.
func libKind(kind string) (string, bool) {
	switch kind {
	case builderapi.LibSkills:
		return catalogindex.KindSkill, true
	case builderapi.LibToolServers:
		return catalogindex.KindMCPServer, true
	}
	return "", false
}

func validKind(w http.ResponseWriter, req *http.Request) (string, bool) {
	k := req.PathValue("kind")
	for _, v := range builderapi.LibraryKinds {
		if v == k {
			return k, true
		}
	}
	fail(w, 404, "not_found", "Unknown library kind.")
	return "", false
}

// usage finds what references a skill or tool server.
type usage struct {
	agents   []file
	launches []file
	projects []file
}

func (r *Reader) usage(req *http.Request, kind, id string) (usage, bool) {
	ix, ok := r.usageIndex(req.Context())
	if !ok {
		return usage{}, false
	}
	return ix.of(kind, id), true
}

// usageIndex holds the agents, launches and projects once, so a list does not reload them per item.
type usageIndex struct{ agents, launches, projects []file }

func (r *Reader) usageIndex(ctx context.Context) (usageIndex, bool) {
	var ix usageIndex
	var ok1, ok2, ok3 bool
	ix.agents, ok1 = r.kind2(ctx, catalogindex.KindAgent)
	ix.launches, ok2 = r.kind2(ctx, catalogindex.KindLaunch)
	ix.projects, ok3 = r.kind2(ctx, catalogindex.KindProject)
	return ix, ok1 && ok2 && ok3
}

func (ix usageIndex) of(kind, id string) usage {
	field := "skills"
	if kind == builderapi.LibToolServers {
		field = "mcp_servers"
	}
	var u usage
	using := map[string]bool{}
	for _, a := range ix.agents {
		for _, s := range strs(a.Fields[field]) {
			if s == id {
				u.agents = append(u.agents, a)
				using[a.ID] = true
				break
			}
		}
	}
	for _, l := range ix.launches {
		if using[str(l.Fields["agent"])] {
			u.launches = append(u.launches, l)
		}
	}
	if kind == builderapi.LibToolServers {
		for _, p := range ix.projects {
			for _, s := range strs(p.Fields["mcp_servers"]) {
				if s == id {
					u.projects = append(u.projects, p)
					break
				}
			}
		}
	}
	return u
}

func (r *Reader) kind2(ctx context.Context, kind string) ([]file, bool) {
	fs, err := loadAll(ctx, r.Index, kind)
	return fs, err == nil
}

func (r *Reader) libItem(f file, kind string, used int, tools map[string]ToolHealth) map[string]any {
	row := map[string]any{"kind": kind, "id": f.ID, "name": orDefault(f.Name, f.ID), "used_by_count": used,
		"layer": f.Layer, "source": "catalog", "path": f.Path, "sha": orNull(f.SHA)}
	if kind == builderapi.LibSkills {
		row["summary"] = str(f.Fields["description"])
		row["read_only"], row["editable"] = false, true
		return row
	}
	// Tool servers are listed but never read (they hold credentials): presence only, no health from here.
	row["summary"] = ""
	row["transport"], row["tags"], row["status"], row["health"] = nil, []string{}, "unknown", nil
	row["tool_count"], row["restarts"], row["scopes"] = nil, nil, nil // scopes are not readable (credentials)
	if h, ok := tools[f.ID]; ok {
		row["transport"], row["tags"], row["health"] = orNull(h.Transport), nonNilStrings(h.Tags), orNull(h.Health)
		row["status"] = orDefault(h.Status, "unknown")
		row["tool_count"], row["restarts"] = h.ToolCount, h.Restarts
	}
	row["read_only"], row["editable"] = true, false
	return row
}

func (r *Reader) libraryList(w http.ResponseWriter, req *http.Request) {
	kind, ok := validKind(w, req)
	if !ok {
		return
	}
	if _, isPart := partKindOf(kind); isPart {
		r.partList(w, req, kind)
		return
	}
	ck, backed := libKind(kind)
	items := []map[string]any{}
	if backed {
		fs, ok := r.kind(w, req, ck)
		if !ok {
			return
		}
		tools := r.toolHealth(req.Context(), kind)
		ix, ok := r.usageIndex(req.Context())
		if !ok {
			fail(w, 503, "catalog_unavailable", "The catalog index could not be read.")
			return
		}
		q := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("q")))
		for _, f := range fs {
			row := r.libItem(f, kind, len(ix.of(kind, f.ID).agents), tools)
			if q != "" && !strings.Contains(strings.ToLower(f.ID+" "+str(row["name"])+" "+str(row["summary"])), q) {
				continue
			}
			items = append(items, row)
		}
	}
	write(w, 200, map[string]any{"items": items})
}

func (r *Reader) libFile(w http.ResponseWriter, req *http.Request) (string, file, bool) {
	kind, ok := validKind(w, req)
	if !ok {
		return "", file{}, false
	}
	ck, backed := libKind(kind)
	if !backed {
		fail(w, 404, "not_found", "No such library item.")
		return "", file{}, false
	}
	fs, ok := r.kind(w, req, ck)
	if !ok {
		return "", file{}, false
	}
	f, found := find(fs, req.PathValue("id"))
	if !found {
		fail(w, 404, "not_found", "No such library item.")
		return "", file{}, false
	}
	return kind, f, true
}

func (r *Reader) libraryItem(w http.ResponseWriter, req *http.Request) {
	if k := req.PathValue("kind"); isPartKind(k) {
		r.partItem(w, req, k)
		return
	}
	kind, f, ok := r.libFile(w, req)
	if !ok {
		return
	}
	u, ok := r.usage(req, kind, f.ID)
	if !ok {
		fail(w, 503, "catalog_unavailable", "The catalog index could not be read.")
		return
	}
	row := r.libItem(f, kind, len(u.agents), r.toolHealth(req.Context(), kind))
	if kind == builderapi.LibSkills {
		prev, trunc := f.Content, false
		if len(prev) > previewBytes {
			prev, trunc = prev[:previewBytes], true
			for !utf8.ValidString(prev) {
				prev = prev[:len(prev)-1]
			}
		}
		row["snapshot_seq"], row["body_preview"], row["body_truncated"] = f.SnapSeq, prev, trunc
	}
	write(w, 200, row)
}

func (r *Reader) libraryUsedBy(w http.ResponseWriter, req *http.Request) {
	if k := req.PathValue("kind"); isPartKind(k) {
		r.partUsedBy(w, req, k)
		return
	}
	kind, f, ok := r.libFile(w, req)
	if !ok {
		return
	}
	u, ok := r.usage(req, kind, f.ID)
	if !ok {
		fail(w, 503, "catalog_unavailable", "The catalog index could not be read.")
		return
	}
	agents, launches, projects := []map[string]any{}, []map[string]any{}, []map[string]any{}
	for _, a := range u.agents {
		agents = append(agents, map[string]any{"id": a.ID, "name": orDefault(a.Name, a.ID)})
	}
	for _, l := range u.launches {
		launches = append(launches, map[string]any{"id": l.ID})
	}
	for _, p := range u.projects {
		projects = append(projects, map[string]any{"id": p.ID})
	}
	write(w, 200, map[string]any{"agents": agents, "launches": launches, "projects": projects,
		"counts": map[string]int{"agents": len(agents), "launches": len(launches), "projects": len(projects)}})
}

func (r *Reader) libraryHistory(w http.ResponseWriter, req *http.Request) {
	if k := req.PathValue("kind"); isPartKind(k) {
		r.partHistory(w, req, k)
		return
	}
	kind, f, ok := r.libFile(w, req)
	if !ok {
		return
	}
	entries := []map[string]any{}
	if kind == builderapi.LibSkills { // tool servers are never read, so they have no snapshots
		snaps, err := loadSnaps(req.Context(), r.Index, f.Path, catalogindex.SnapshotsPerPath)
		if err != nil {
			fail(w, 503, "catalog_unavailable", "The catalog index could not be read.")
			return
		}
		for _, s := range snaps {
			sum := "Observed change"
			if s.Source == "builder" {
				sum = f.Path + " changed"
			}
			entries = append(entries, map[string]any{"revision": nil, "snapshot_seq": s.Seq, "at": s.At.UnixMilli(),
				"actor": orNull(s.Actor), "plan_id": orNull(s.PlanID), "sha": orNull(s.SHA), "summary": sum})
		}
	}
	out, next := page(req, entries)
	if out == nil {
		out = []map[string]any{}
	}
	write(w, 200, map[string]any{"entries": out, "next_cursor": orNull(next)})
}

// resolved is the Resolved card computed from the catalog index. Tether's own resolution is not used (its route is
// unverified), so resolved_by is always tachyon_approx and the card says what it could not know.
func (r *Reader) resolved(w http.ResponseWriter, req *http.Request) {
	as, ok := r.kind(w, req, catalogindex.KindAgent)
	if !ok {
		return
	}
	root, found := find(as, req.PathValue("id"))
	if !found {
		fail(w, 404, "not_found", "No such agent.")
		return
	}
	skills, _ := loadAll(req.Context(), r.Index, catalogindex.KindSkill)
	servers, _ := loadAll(req.Context(), r.Index, catalogindex.KindMCPServer)
	notes := []string{"Approximate: computed from the catalog index, not by Tether. Tool server health and tool counts are not known here."}

	chain, seen := []file{}, map[string]bool{}
	var walk func(f file, depth int)
	walk = func(f file, depth int) {
		if seen[f.ID] || depth > 10 {
			return
		}
		seen[f.ID] = true
		chain = append(chain, f)
		for _, p := range strs(f.Fields["extends"]) {
			if pf, ok := find(as, p); ok {
				walk(pf, depth+1)
			} else {
				notes = append(notes, "Parent agent "+p+" is not in the catalog.")
			}
		}
	}
	walk(root, 0)

	launchID := any(nil)
	extraServers := []string{}
	if lid := req.URL.Query().Get("launch"); lid != "" {
		ls, ok := r.kind(w, req, catalogindex.KindLaunch)
		if !ok {
			return
		}
		l, lf := find(ls, lid)
		if !lf {
			fail(w, 404, "not_found", "No such launch.")
			return
		}
		if str(l.Fields["agent"]) != root.ID {
			fail(w, 400, "invalid", "That launch belongs to another agent.")
			return
		}
		launchID = l.ID
		ps, _ := loadAll(req.Context(), r.Index, catalogindex.KindProject)
		if p, ok := find(ps, str(l.Fields["project"])); ok {
			extraServers = strs(p.Fields["mcp_servers"])
		}
	}

	ids := func(field string, extra []string) []string {
		var out []string
		have := map[string]bool{}
		for _, f := range chain {
			for _, s := range strs(f.Fields[field]) {
				if !have[s] {
					have[s] = true
					out = append(out, s)
				}
			}
		}
		for _, s := range extra {
			if !have[s] {
				have[s] = true
				out = append(out, s)
			}
		}
		return out
	}
	skillRows := []map[string]any{}
	for _, id := range ids("skills", nil) {
		name := id
		if s, ok := find(skills, id); ok {
			name = orDefault(s.Name, id)
		} else {
			notes = append(notes, "Skill "+id+" is not in the catalog.")
		}
		skillRows = append(skillRows, map[string]any{"id": id, "name": name})
	}
	serverRows := []map[string]any{}
	for _, id := range ids("mcp_servers", extraServers) {
		status := "unknown"
		if _, ok := find(servers, id); !ok {
			status = "missing"
		}
		serverRows = append(serverRows, map[string]any{"id": id, "status": status, "tool_count": nil})
	}

	// Policy: the nearest definition in the chain wins.
	policy := map[string]any{"permission_mode": nil, "network": nil, "default_sandbox": nil}
	scopes := []string{}
	haveScope := map[string]bool{}
	for _, f := range chain {
		d := doc(f)
		perm := sub(d, "permissions")
		for _, k := range []string{"permission_mode", "network", "default_sandbox"} {
			if policy[k] == nil && str(perm[k]) != "" {
				policy[k] = str(perm[k])
			}
		}
		for _, sc := range strs(perm["mcp_scopes"]) {
			if !haveScope[sc] {
				haveScope[sc] = true
				scopes = append(scopes, sc)
			}
		}
	}
	sort.Strings(scopes)

	parts := []map[string]any{}
	total := 0
	for i := len(chain) - 1; i >= 0; i-- {
		f := chain[i]
		d := doc(f)
		pr := sub(d, "prompt")
		for _, p := range []struct{ label, text string }{
			{"system_prompt", str(pr["system_prompt"])}, {"agent_prompt", str(pr["agent_prompt"])},
			{"body", firstStr(str(pr["body"]), str(d["prompt"]))},
		} {
			if p.text == "" {
				continue
			}
			label := p.label
			if i != 0 {
				label = f.ID + " " + p.label
			}
			tok := (len(p.text) + 3) / 4
			total += tok
			parts = append(parts, map[string]any{"label": label, "bytes": len(p.text), "tokens_est": tok})
		}
	}
	write(w, 200, map[string]any{"agent_id": root.ID, "launch_id": launchID, "resolved_by": builderapi.ResolvedByApprox,
		"tool_servers": serverRows, "skills": skillRows, "policy": policy, "scopes": scopes, "prompt_parts": parts,
		"prompt_tokens_est": total, "prompt_tokens_estimated": true, "extends_chain": chainIDs(chain), "notes": notes})
}

func chainIDs(c []file) []string {
	out := make([]string, len(c))
	for i, f := range c {
		out[i] = f.ID
	}
	return out
}

func isPartKind(k string) bool { _, ok := partKindOf(k); return ok }
