package builderread

import (
	"errors"
	"testing"

	"github.com/hollis-labs/tachyon/internal/builderapi"
)

func TestRegistryComposesAndLinks(t *testing.T) {
	sess := fakeSessions{list: []LiveSession{{ID: "s1", LaunchID: "builder-main", AgentID: "builder"}}}
	r, mux, _, _ := fixture(t, sess)
	r.Projects = fakeProjects{list: []RegProject{
		{URN: "msg://project/tachyon", DisplayName: "Tachyon", Description: "The console.", Status: "active", Tags: []string{"app"}, Guidelines: "Stubs first.", EntryPoints: []string{"cmd/tachyon/main.go"}},
		{URN: "msg://project/p_9", DisplayName: "Scratch notes", Status: "archived"},
		{URN: "msg://project/old", DisplayName: "Old", Status: "merged", MergedInto: "msg://project/tachyon"},
		{URN: "msg://project/x", DisplayName: "Mapped", Props: map[string]string{"catalog_id": "other"}},
	}}
	const reg = builderapi.RegistryPrefix + "/projects"

	rows := getAt(t, mux, reg, 200)["projects"].([]any)
	if len(rows) != 3 {
		t.Fatalf("merged projects are folded away: %v", rows)
	}
	byID := map[string]map[string]any{}
	for _, x := range rows {
		byID[x.(map[string]any)["id"].(string)] = x.(map[string]any)
	}
	tach := byID["tachyon"]
	if field(tach, "link", "state") != "linked" || field(tach, "link", "catalog_id") != "tachyon" ||
		field(tach, "counts", "agents").(float64) != 1 || field(tach, "counts", "launches").(float64) != 1 ||
		field(tach, "counts", "tether_sessions").(float64) != 1 || field(tach, "counts", "planner_items") != nil {
		t.Fatalf("linked row: %v", tach)
	}
	scratch := byID["p_9"]
	if field(scratch, "link", "state") != "unlinked" || field(scratch, "link", "reason") == nil || field(scratch, "counts", "agents") != nil {
		t.Fatalf("unlinked row must carry a reason and null counts: %v", scratch)
	}
	if field(byID["x"], "link", "catalog_id") != "other" {
		t.Fatalf("props.catalog_id should link: %v", byID["x"])
	}

	d := getAt(t, mux, reg+"/tachyon", 200)
	if field(d, "identity", "display_name") != "Tachyon" || field(d, "guidance", "guidelines") != "Stubs first." ||
		field(d, "catalog", "sha") == "" || len(field(d, "catalog", "mcp_servers").([]any)) != 2 {
		t.Fatalf("detail: %v", d)
	}
	cb := d["consumed_by"].(map[string]any)
	if len(cb["agents"].([]any)) != 1 || len(cb["launches"].([]any)) != 1 || field(cb, "tether_sessions", "live").(float64) != 1 ||
		field(cb, "tether_sessions", "total") != nil || cb["planner_items"] != nil {
		t.Fatalf("consumed_by: %v", cb)
	}
	un := getAt(t, mux, reg+"/p_9", 200)
	if un["catalog"] != nil || field(un, "link", "state") != "unlinked" || field(un, "consumed_by", "agents") != nil {
		t.Fatalf("unlinked detail: %v", un)
	}
	getAt(t, mux, reg+"/nope", 404)
	if q := getAt(t, mux, reg+"?q=scratch", 200)["projects"].([]any); len(q) != 1 {
		t.Fatalf("q: %v", q)
	}
}

func TestRegistryUnavailableAndAmbiguous(t *testing.T) {
	r, mux, _, _ := fixture(t, nil)
	const reg = builderapi.RegistryPrefix + "/projects"
	getAt(t, mux, reg, 503) // no source configured
	r.Projects = fakeProjects{err: errors.New("down")}
	if m := getAt(t, mux, reg+"/tachyon", 503); m["code"] != "registry_unavailable" {
		t.Fatalf("an unreadable registry is 503, not an empty list: %v", m)
	}
	// Two different catalog projects named by props and an external id: no guessing.
	r.Projects = fakeProjects{list: []RegProject{{URN: "msg://project/z", DisplayName: "Z", Props: map[string]string{"catalog_id": "tachyon"},
		ExternalIDs: []ExternalID{{Substrate: "tether-catalog", ID: "other"}}}}}
	got := getAt(t, mux, reg, 200)["projects"].([]any)[0].(map[string]any)
	if field(got, "link", "state") != "unlinked" || field(got, "link", "catalog_id") != nil {
		t.Fatalf("ambiguous must read unlinked: %v", got["link"])
	}
}

func TestSlugAndIDs(t *testing.T) {
	for in, want := range map[string]string{"Agent OS": "agent-os", "  Tachyon ": "tachyon", "!!!": "", "A_b.c": "a_b.c"} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
	if regID(RegProject{URN: "msg://project/Alpha", DisplayName: "Other"}) != "alpha" ||
		regID(RegProject{Props: map[string]string{"slug": "pinned"}, URN: "msg://project/x"}) != "pinned" {
		t.Error("regID order")
	}
}

// field walks nested JSON objects: field(m, "link", "state").
func field(m map[string]any, path ...string) any {
	var v any = m
	for _, p := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[p]
	}
	return v
}
