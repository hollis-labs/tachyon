package builderread

import (
	"context"
	"errors"
	"testing"
)

type fakeProjects struct {
	list []RegProject
	err  error
}

func (f fakeProjects) Projects(context.Context) ([]RegProject, error) { return f.list, f.err }

type fakeTools struct {
	m   map[string]ToolHealth
	err error
}

func (f fakeTools) Tools(context.Context) (map[string]ToolHealth, error) { return f.m, f.err }

func TestToolServerHealthFromASourceAndUnknownWithout(t *testing.T) {
	r, mux, _, _ := fixture(t, nil)
	n, rs := 48, 3
	r.Tools = fakeTools{m: map[string]ToolHealth{"tether": {Transport: "http", Status: "degraded", Health: "slow", Tags: []string{"core"}, ToolCount: &n, Restarts: &rs}}}
	row := get(t, mux, "/library/tool_servers", 200)["items"].([]any)[0].(map[string]any)
	if row["status"] != "degraded" || row["transport"] != "http" || row["health"] != "slow" || row["tool_count"].(float64) != 48 || row["restarts"].(float64) != 3 {
		t.Fatalf("with a source: %v", row)
	}
	if row["scopes"] != nil || row["read_only"] != true {
		t.Fatalf("scopes stay unreadable and the row read-only: %v", row)
	}
	if d := get(t, mux, "/library/tool_servers/tether", 200); d["status"] != "degraded" {
		t.Fatalf("detail: %v", d)
	}
	r.Tools = fakeTools{err: errors.New("down")}
	row = get(t, mux, "/library/tool_servers", 200)["items"].([]any)[0].(map[string]any)
	if row["status"] != "unknown" || row["tool_count"] != nil {
		t.Fatalf("a failing source must read unknown, not ok: %v", row)
	}
	r.Tools = nil
	row = get(t, mux, "/library/tool_servers", 200)["items"].([]any)[0].(map[string]any)
	if row["status"] != "unknown" {
		t.Fatalf("no source: %v", row)
	}
	// Skills never take health from the tools source.
	r.Tools = fakeTools{m: map[string]ToolHealth{"commit": {Status: "down"}}}
	if sk := get(t, mux, "/library/skills", 200)["items"].([]any)[0].(map[string]any); sk["status"] != nil {
		t.Fatalf("skill row: %v", sk)
	}
}

func TestCheckUnreferencedRefusesWhileInUse(t *testing.T) {
	r, _, src, ix := fixture(t, nil)
	ctx := context.Background()
	err := CheckUnreferenced(ctx, r, "skills", "commit")
	var ref *Referenced
	if !errors.As(err, &ref) || len(ref.Agents) != 2 || len(ref.Launches) != 1 || ref.Code() != "referenced" {
		t.Fatalf("skill in use: %v", err)
	}
	err = CheckUnreferenced(ctx, r, "tool_servers", "tether")
	if !errors.As(err, &ref) || len(ref.Agents) != 1 || len(ref.Projects) != 1 {
		t.Fatalf("tool server in use: %v", err)
	}
	if err := CheckUnreferenced(ctx, r, "skills", "unused-skill"); err != nil {
		t.Fatalf("unreferenced skill should be free: %v", err)
	}
	if err := CheckUnreferenced(ctx, r, "prompts", "x"); err == nil {
		t.Fatal("kinds without catalog references must not read as free")
	}
	// Once the last reference goes, the skill is free.
	src.files["agents/builder.yaml"] = "id: builder\nextends: base\n"
	src.files["agents/base.yaml"] = "id: base\nabstract: true\n"
	src.plan = "plan_free"
	if err := ix.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if err := CheckUnreferenced(ctx, r, "skills", "commit"); err != nil {
		t.Fatalf("after removing references: %v", err)
	}
	// An unreadable index must refuse, not report free.
	dead, cancel := context.WithCancel(ctx)
	cancel()
	if err := CheckUnreferenced(dead, r, "skills", "commit"); err == nil {
		t.Fatal("unknown must not read as free")
	}
}
