package builderread

import (
	"context"
	"strings"
	"testing"
)

func TestLibraryListAndDetail(t *testing.T) {
	_, mux, _, _ := fixture(t, nil)
	sk := get(t, mux, "/library/skills", 200)["items"].([]any)
	if len(sk) != 1 {
		t.Fatalf("skills: %v", sk)
	}
	row := sk[0].(map[string]any)
	if row["id"] != "commit" || row["name"] != "Commit" || row["summary"] != "small commits" || row["used_by_count"].(float64) != 2 ||
		row["source"] != "catalog" || row["path"] != "skills/commit.md" || row["editable"] != true {
		t.Fatalf("skill row: %v", row)
	}
	ts := get(t, mux, "/library/tool_servers", 200)["items"].([]any)
	if len(ts) != 1 || ts[0].(map[string]any)["status"] != "unknown" || ts[0].(map[string]any)["read_only"] != true || ts[0].(map[string]any)["used_by_count"].(float64) != 1 {
		t.Fatalf("tool servers: %v", ts)
	}
	for _, k := range []string{"prompts", "policies"} {
		if it := get(t, mux, "/library/"+k, 200)["items"].([]any); len(it) != 0 {
			t.Fatalf("%s should be empty until B3: %v", k, it)
		}
		get(t, mux, "/library/"+k+"/x", 404)
	}
	get(t, mux, "/library/nope", 404)
	if q := get(t, mux, "/library/skills?q=zzz", 200)["items"].([]any); len(q) != 0 {
		t.Fatalf("q filter: %v", q)
	}

	d := get(t, mux, "/library/skills/commit", 200)
	if d["snapshot_seq"].(float64) != 1 || d["body_truncated"] != false || !strings.Contains(d["body_preview"].(string), "Body") {
		t.Fatalf("skill detail: %v", d)
	}
	get(t, mux, "/library/skills/ghost-skill", 404)
	if td := get(t, mux, "/library/tool_servers/tether", 200); td["editable"] != false {
		t.Fatalf("tool server detail: %v", td)
	}
}

func TestLibraryUsedByAndHistory(t *testing.T) {
	_, mux, src, ix := fixture(t, nil)
	u := get(t, mux, "/library/skills/commit/used_by", 200)
	if c := u["counts"].(map[string]any); c["agents"].(float64) != 2 || c["launches"].(float64) != 1 {
		t.Fatalf("skill used_by: %v", u)
	}
	tu := get(t, mux, "/library/tool_servers/tether/used_by", 200)
	if c := tu["counts"].(map[string]any); c["agents"].(float64) != 1 || c["projects"].(float64) != 1 {
		t.Fatalf("tool server used_by: %v", tu)
	}
	src.files["skills/commit.md"] += "More\n"
	src.plan = "plan_2"
	if err := ix.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := get(t, mux, "/library/skills/commit/history", 200)["entries"].([]any)
	if len(h) != 2 || h[0].(map[string]any)["snapshot_seq"].(float64) != 2 || h[0].(map[string]any)["sha"] == nil {
		t.Fatalf("history: %v", h)
	}
	if th := get(t, mux, "/library/tool_servers/tether/history", 200)["entries"].([]any); len(th) != 0 {
		t.Fatalf("tool servers are never read, so no history: %v", th)
	}
}

func TestResolvedChainPolicyAndLaunch(t *testing.T) {
	_, mux, _, _ := fixture(t, nil)
	m := get(t, mux, "/agents/builder/resolved", 200)
	if m["resolved_by"] != "tachyon_approx" || m["launch_id"] != nil || m["prompt_tokens_estimated"] != true {
		t.Fatalf("resolved: %v", m)
	}
	if c := m["extends_chain"].([]any); len(c) != 2 || c[0] != "builder" || c[1] != "base" {
		t.Fatalf("chain: %v", c)
	}
	p := m["policy"].(map[string]any)
	if p["permission_mode"] != "acceptEdits" || p["network"] != "restricted" || p["default_sandbox"] != nil {
		t.Fatalf("policy (child wins, parent fills gaps): %v", p)
	}
	if sc := m["scopes"].([]any); len(sc) != 1 || sc[0] != "tether:read" {
		t.Fatalf("scopes: %v", sc)
	}
	skills := m["skills"].([]any)
	if len(skills) != 2 || skills[0].(map[string]any)["name"] != "Commit" {
		t.Fatalf("skills (deduped, missing kept by id): %v", skills)
	}
	notes := strings.Join(anyStrings(m["notes"]), "|")
	if !strings.Contains(notes, "ghost-skill") || !strings.Contains(notes, "Approximate") {
		t.Fatalf("notes: %s", notes)
	}
	parts := m["prompt_parts"].([]any)
	if len(parts) != 1 || parts[0].(map[string]any)["label"] != "base system_prompt" || m["prompt_tokens_est"].(float64) != 3 {
		t.Fatalf("prompt parts: %v tokens %v", parts, m["prompt_tokens_est"])
	}
	if srv := m["tool_servers"].([]any); len(srv) != 1 || srv[0].(map[string]any)["status"] != "unknown" {
		t.Fatalf("servers: %v", srv)
	}

	l := get(t, mux, "/agents/builder/resolved?launch=builder-main", 200)
	srv := l["tool_servers"].([]any)
	if l["launch_id"] != "builder-main" || len(srv) != 2 || srv[1].(map[string]any)["id"] != "extra" || srv[1].(map[string]any)["status"] != "missing" {
		t.Fatalf("launch adds the project's servers: %v", l)
	}
	get(t, mux, "/agents/builder/resolved?launch=nope", 404)
	get(t, mux, "/agents/base/resolved?launch=builder-main", 400)
	get(t, mux, "/agents/nope/resolved", 404)
}

func TestResolvedSurvivesAnExtendsCycle(t *testing.T) {
	_, mux, src, ix := fixture(t, nil)
	src.files["agents/base.yaml"] = "id: base\nextends: builder\n"
	src.plan = "plan_cycle"
	if err := ix.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := get(t, mux, "/agents/builder/resolved", 200)
	if c := m["extends_chain"].([]any); len(c) != 2 {
		t.Fatalf("cycle must terminate with each agent once: %v", c)
	}
}

func anyStrings(v any) []string {
	var out []string
	for _, e := range v.([]any) {
		out = append(out, e.(string))
	}
	return out
}
