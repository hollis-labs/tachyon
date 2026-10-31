package builderread

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/hollis-labs/tachyon/internal/builderapi"
	"github.com/hollis-labs/tachyon/internal/builderparts"
	"github.com/hollis-labs/tachyon/internal/catalogindex"
)

// Library prompts and policies are Tachyon-owned parts (B5): rows in the local store, compiled into agent files.
// They are read here with the agents that carry them, and drift is the carrying file against the hash recorded
// when it was compiled.

func partKindOf(libKind string) (string, bool) {
	switch libKind {
	case builderapi.LibPrompts:
		return builderparts.KindPrompt, true
	case builderapi.LibPolicies:
		return builderparts.KindPolicy, true
	}
	return "", false
}

func bundleKeys(p builderparts.Part) []string {
	out := make([]string, 0, len(p.Bundle))
	for k := range p.Bundle {
		if k != builderparts.RulesKey { // rules are never compiled into an agent file
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// carriers lists the agents that carry a part with their drift state, using the agents already loaded.
func carriers(ctx context.Context, r *Reader, p builderparts.Part, agents []file) []map[string]any {
	refs, _ := builderparts.RefsOfPart(ctx, r.Index.DB.Reader, p.Kind, p.ID)
	byID := map[string]file{}
	for _, a := range agents {
		byID[a.ID] = a
	}
	out := []map[string]any{}
	for _, ref := range refs {
		a, ok := byID[ref.AgentID]
		drifted := ok && a.Content != "" && builderparts.Drifted(a.Content, ref, bundleKeys(p))
		out = append(out, map[string]any{"id": ref.AgentID, "rev": ref.Rev, "drifted": drifted})
	}
	return out
}

func partRow(p builderparts.Part, lib string, used int) map[string]any {
	row := map[string]any{"kind": lib, "id": p.ID, "name": orDefault(p.Name, p.ID), "summary": p.Summary, "used_by_count": used,
		"revision": p.Revision, "archived": p.Archived, "updated_at": p.UpdatedNS / 1e6, "editable": !p.Archived}
	if p.Kind == builderparts.KindPrompt {
		row["bytes"], row["tokens_est"] = len(p.Text), (len(p.Text)+3)/4
		return row
	}
	row["permission_mode"], row["network"], row["default_sandbox"] = orNull(str(p.Bundle["permission_mode"])), orNull(str(p.Bundle["network"])), orNull(str(p.Bundle["default_sandbox"]))
	row["scope_count"] = len(strs(p.Bundle["mcp_scopes"]))
	priv := false
	for k := range p.Bundle {
		priv = priv || builderapi.PrivilegedPath("policy."+k)
	}
	row["privileged"] = priv
	return row
}

func (r *Reader) partList(w http.ResponseWriter, req *http.Request, lib string) {
	kind, _ := partKindOf(lib)
	ps, err := builderparts.List(req.Context(), r.Index.DB.Reader, kind)
	if err != nil {
		fail(w, 503, "catalog_unavailable", "The library could not be read.")
		return
	}
	q := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("q")))
	items := []map[string]any{}
	for _, p := range ps {
		refs, _ := builderparts.RefsOfPart(req.Context(), r.Index.DB.Reader, kind, p.ID)
		row := partRow(p, lib, len(refs))
		if q != "" && !strings.Contains(strings.ToLower(p.ID+" "+str(row["name"])+" "+p.Summary), q) {
			continue
		}
		items = append(items, row)
	}
	write(w, 200, map[string]any{"items": items})
}

func (r *Reader) loadPart(w http.ResponseWriter, req *http.Request, lib string) (builderparts.Part, bool) {
	kind, _ := partKindOf(lib)
	p, ok, err := builderparts.Get(req.Context(), r.Index.DB.Reader, kind, req.PathValue("id"))
	if err != nil {
		fail(w, 503, "catalog_unavailable", "The library could not be read.")
		return p, false
	}
	if !ok {
		fail(w, 404, "not_found", "No such library item.")
		return p, false
	}
	return p, true
}

func (r *Reader) partItem(w http.ResponseWriter, req *http.Request, lib string) {
	p, ok := r.loadPart(w, req, lib)
	if !ok {
		return
	}
	agents, _ := loadAll(req.Context(), r.Index, catalogindex.KindAgent)
	refs := carriers(req.Context(), r, p, agents)
	row := partRow(p, lib, len(refs))
	if p.Kind == builderparts.KindPrompt {
		row["body"] = p.Text
	} else {
		row["bundle"] = p.Bundle
	}
	row["referencing_agents"] = refs
	write(w, 200, row)
}

func (r *Reader) partUsedBy(w http.ResponseWriter, req *http.Request, lib string) {
	p, ok := r.loadPart(w, req, lib)
	if !ok {
		return
	}
	agents, _ := loadAll(req.Context(), r.Index, catalogindex.KindAgent)
	rows, ids := []map[string]any{}, map[string]bool{}
	for _, c := range carriers(req.Context(), r, p, agents) {
		id := str(c["id"])
		ids[id] = true
		name := id
		if a, ok := find(agents, id); ok {
			name = orDefault(a.Name, id)
		}
		rows = append(rows, map[string]any{"id": id, "name": name})
	}
	launches := []map[string]any{}
	if ls, ok := r.kind2(req.Context(), catalogindex.KindLaunch); ok {
		for _, l := range ls {
			if ids[str(l.Fields["agent"])] {
				launches = append(launches, map[string]any{"id": l.ID})
			}
		}
	}
	write(w, 200, map[string]any{"agents": rows, "launches": launches, "projects": []map[string]any{},
		"counts": map[string]int{"agents": len(rows), "launches": len(launches), "projects": 0}})
}

func (r *Reader) partHistory(w http.ResponseWriter, req *http.Request, lib string) {
	p, ok := r.loadPart(w, req, lib)
	if !ok {
		return
	}
	revs, err := builderparts.Revisions(req.Context(), r.Index.DB.Reader, p.Kind, p.ID)
	if err != nil {
		fail(w, 503, "catalog_unavailable", "The library could not be read.")
		return
	}
	entries := []map[string]any{}
	for _, v := range revs {
		sum := "Revision " + itoa(v.Revision)
		if v.Revision == 1 {
			sum = "Created"
		}
		entries = append(entries, map[string]any{"revision": v.Revision, "snapshot_seq": nil, "at": v.AtNS / 1e6,
			"actor": orNull(v.Actor), "plan_id": orNull(v.Plan), "sha": nil, "summary": sum})
	}
	out, next := page(req, entries)
	if out == nil {
		out = []map[string]any{}
	}
	write(w, 200, map[string]any{"entries": out, "next_cursor": orNull(next)})
}

// agentParts is the parts[] of an agent detail, and the prompt parts for its prompt section.
func (r *Reader) agentParts(ctx context.Context, f file) (all, prompts []map[string]any) {
	all, prompts = []map[string]any{}, []map[string]any{}
	refs, err := builderparts.RefsOfAgent(ctx, r.Index.DB.Reader, f.ID)
	if err != nil {
		return
	}
	for _, ref := range refs {
		p, ok, _ := builderparts.Get(ctx, r.Index.DB.Reader, ref.Kind, ref.PartID)
		drifted := ok && f.Content != "" && builderparts.Drifted(f.Content, ref, bundleKeys(p))
		all = append(all, map[string]any{"kind": ref.Kind, "id": ref.PartID, "rev": ref.Rev, "drifted": drifted, "compiled_hash": ref.Hash})
		if ref.Kind == builderparts.KindPrompt {
			prompts = append(prompts, map[string]any{"kind": ref.Kind, "id": ref.PartID, "rev": ref.Rev, "label": orDefault(p.Name, ref.PartID),
				"bytes": len(p.Text), "tokens_est": (len(p.Text) + 3) / 4})
		}
	}
	return
}
