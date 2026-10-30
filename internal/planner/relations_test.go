package planner

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/objects"
)

// wire round-trips a value through JSON, as a client sees it.
func wire(t *testing.T, v Item) Item {
	t.Helper()
	m, err := decodeMap(encode(v))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mk(t *testing.T, s *Owned, db, title string, extra Item) Item {
	t.Helper()
	f := Item{"title": title}
	for k, v := range extra {
		f[k] = v
	}
	row, err := s.Create(context.Background(), db, uuid.NewString(), f, "operator:test")
	if err != nil {
		t.Fatal(db, title, err)
	}
	return row
}

func TestPinStampsAndOrdersFirst(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	a := mk(t, s, "ideas", "a", Item{"order": 10})
	b := mk(t, s, "ideas", "b", Item{"order": 20})
	pinned, err := s.Pin(ctx, "ideas", b["id"].(string), revision(b), true, "operator:test")
	if err != nil || pinned["pinned"] != true || textField(pinned, "pinned_at") == "" {
		t.Fatal(pinned, err)
	}
	list, err := s.List(ctx, "ideas", nil, "")
	if err != nil || list[0]["id"] != b["id"] || list[1]["id"] != a["id"] {
		t.Fatal("pinned item must sort first", list, err)
	}
	if only, _ := s.List(ctx, "ideas", map[string]string{"pinned": "false"}, ""); len(only) != 1 || only[0]["id"] != a["id"] {
		t.Fatal("pinned=false must match items that were never pinned", only)
	}
	if _, err = s.Pin(ctx, "ideas", b["id"].(string), revision(b), false, "operator:test"); !errors.Is(err, objects.ErrConflict) {
		t.Fatal("stale revision must conflict", err)
	}
	cleared, err := s.Pin(ctx, "ideas", b["id"].(string), revision(pinned), false, "operator:test")
	if err != nil || cleared["pinned"] != nil || cleared["pinned_at"] != nil {
		t.Fatal(cleared, err)
	}
	if _, err = s.Patch(ctx, "ideas", a["id"].(string), revision(a), Item{"pinned_at": "2026-01-01T00:00:00Z"}, "operator:test"); !errors.Is(err, objects.ErrInvalid) {
		t.Fatal("pinned_at is server managed", err)
	}
	if _, err = s.Patch(ctx, "ideas", a["id"].(string), revision(a), Item{"pinned": "yes"}, "operator:test"); !errors.Is(err, objects.ErrInvalid) {
		t.Fatal("pinned must be a boolean", err)
	}
}

func TestThemeTypedFieldsValidated(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	th := mk(t, s, "themes", "Agents", Item{"area": "harness", "horizon": "now", "done_summary": "shipped"})
	if th["area"] != "harness" || th["horizon"] != "now" || th["done_summary"] != "shipped" {
		t.Fatal(th)
	}
	for _, bad := range []Item{{"area": "nope"}, {"horizon": "soon"}, {"done_summary": strings.Repeat("x", 601)}} {
		if _, err := s.Patch(ctx, "themes", th["id"].(string), revision(th), bad, "operator:test"); !errors.Is(err, objects.ErrInvalid) {
			t.Fatal("must reject", bad, err)
		}
	}
	if _, err := s.Create(ctx, "roadmap", uuid.NewString(), Item{"title": "r", "horizon": "later"}, "operator:test"); err != nil {
		t.Fatal("roadmap keeps its own area/horizon fields:", err)
	}
}

func TestRelationsRulesCycleIndexAndLinks(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	id := func(r Item) string { return r["id"].(string) }
	a, b, c := mk(t, s, "themes", "A", nil), mk(t, s, "themes", "B", nil), mk(t, s, "themes", "C", nil)
	rm, rk := mk(t, s, "roadmap", "R", nil), mk(t, s, "risks", "K", nil)
	ref := func(db string, r Item) PortfolioRef { return PortfolioRef{DB: db, ID: id(r)} }

	a1, err := s.AddRelation(ctx, ref("themes", a), "needs", ref("themes", b), revision(a), "operator:test")
	if err != nil || a1["relations_count"] != 1 {
		t.Fatal(a1, err)
	}
	if again, err := s.AddRelation(ctx, ref("themes", a), "needs", ref("themes", b), revision(a1), "operator:test"); err != nil || again["relations_count"] != 1 {
		t.Fatal("adding an existing relation is a no-op", again, err)
	}
	b1, err := s.AddRelation(ctx, ref("themes", b), "needs", ref("themes", c), revision(b), "operator:test")
	if err != nil {
		t.Fatal(err)
	}
	// C needs A would close A -> B -> C -> A.
	if _, err = s.AddRelation(ctx, ref("themes", c), "needs", ref("themes", a), revision(c), "operator:test"); !errors.Is(err, ErrCycle) {
		t.Fatal("cycle must be rejected", err)
	}
	for name, call := range map[string]func() error{
		"self": func() error {
			_, e := s.AddRelation(ctx, ref("themes", c), "relates", ref("themes", c), revision(c), "operator:test")
			return e
		},
		"delivers to risk": func() error {
			_, e := s.AddRelation(ctx, ref("themes", c), "delivers", ref("risks", rk), revision(c), "operator:test")
			return e
		},
		"mitigates to roadmap": func() error {
			_, e := s.AddRelation(ctx, ref("themes", c), "mitigates", ref("roadmap", rm), revision(c), "operator:test")
			return e
		},
		"needs across dbs": func() error {
			_, e := s.AddRelation(ctx, ref("themes", c), "needs", ref("roadmap", rm), revision(c), "operator:test")
			return e
		},
	} {
		if err := call(); !errors.Is(err, ErrRule) {
			t.Fatal(name, "must break a rule", err)
		}
	}
	if _, err = s.AddRelation(ctx, ref("themes", c), "delivers", ref("roadmap", rm), revision(c), "operator:test"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddRelation(ctx, ref("themes", c), "unknown", ref("themes", a), revision(c)+1, "operator:test"); !errors.Is(err, objects.ErrInvalid) {
		t.Fatal(err)
	}

	view, err := s.Links(ctx, "themes", id(b))
	if err != nil {
		t.Fatal(err)
	}
	view = wire(t, view)
	out, in := array(view["outbound"]), array(view["inbound"])
	if len(out) != 1 || object(out[0])["kind"] != "needs" || object(object(out[0])["to"])["id"] != id(c) {
		t.Fatal("outbound", out)
	}
	if len(in) != 1 || object(in[0])["kind"] != "needs" || object(object(in[0])["from"])["id"] != id(a) || object(in[0])["title"] != "A" {
		t.Fatal("inbound comes from the derived index", in)
	}

	// Removal updates the index in the same write, and the cycle is possible once the path is gone.
	if _, err = s.RemoveRelation(ctx, ref("themes", b), "needs", ref("themes", c), revision(b1), "operator:test"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Objects.RelationsTo(ctx, id(c))
	if len(rows) != 0 {
		t.Fatal("index kept a removed edge", rows)
	}
	c2, _ := s.Get(ctx, "themes", id(c))
	if _, err = s.AddRelation(ctx, ref("themes", c), "needs", ref("themes", a), revision(c2), "operator:test"); err != nil {
		t.Fatal("no path left, so no cycle:", err)
	}
	if _, err = s.Patch(ctx, "themes", id(a), 99, Item{"relations": []any{}}, "operator:test"); !errors.Is(err, objects.ErrInvalid) {
		t.Fatal("relations are written only through the relation routes", err)
	}
}

func TestLinksMapsLegacyArraysAndExternal(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	dep := mk(t, s, "themes", "Dep", nil)
	rm := mk(t, s, "roadmap", "RM", nil)
	th := mk(t, s, "themes", "T", Item{
		"depends_on": []any{dep["id"]}, "roadmap_ids": []any{rm["id"]},
		"links": []any{Item{"kind": "pr", "ref": "hollis-labs/tachyon#229", "label": "B6"}, Item{"kind": "url", "ref": "https://example.com"}},
	})
	view, err := s.Links(ctx, "themes", th["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	view = wire(t, view)
	kinds := map[string]string{}
	for _, v := range array(view["outbound"]) {
		kinds[textField(object(v), "title")] = textField(object(v), "kind")
	}
	if kinds["Dep"] != "needs" || kinds["RM"] != "delivers" {
		t.Fatal("legacy arrays must read as typed kinds", kinds)
	}
	ext := array(view["external"])
	pr := object(object(ext[0])["pr"])
	if len(ext) != 2 || pr["owner"] != "hollis-labs" || pr["repo"] != "tachyon" || number(pr["number"]) != 229 {
		t.Fatal("pr cite must parse", ext)
	}
}

func TestIdeaPromoteToTheme(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	idea := mk(t, s, "ideas", "Offline mode", nil)
	target := uuid.NewString()
	res, err := s.PromoteIdea(ctx, idea["id"].(string), revision(idea), "themes", target, Item{"area": "gui"}, "operator:test")
	if err != nil {
		t.Fatal(err)
	}
	res = wire(t, res)
	created, got := object(res["created"]), object(res["idea"])
	if created["id"] != target || created["title"] != "Offline mode" || created["area"] != "gui" || created["db"] != "themes" || got["status"] != "promoted" || got["db"] != "ideas" {
		t.Fatal(res)
	}
	if object(res["receipt"])["at"] == "" {
		t.Fatal("receipt")
	}
	// A replay of the same request returns the same result; a second promote of the idea elsewhere conflicts.
	again, err := s.PromoteIdea(ctx, idea["id"].(string), revision(idea), "themes", target, Item{"area": "gui"}, "operator:test")
	if err != nil || object(wire(t, again)["created"])["id"] != target {
		t.Fatal("replay", again, err)
	}
	if _, err = s.PromoteIdea(ctx, idea["id"].(string), revision(idea), "roadmap", uuid.NewString(), nil, "operator:test"); !errors.Is(err, objects.ErrConflict) {
		t.Fatal("one canonical promotion per idea", err)
	}
	if _, err = s.PromoteIdea(ctx, idea["id"].(string), revision(idea), "risks", uuid.NewString(), nil, "operator:test"); !errors.Is(err, objects.ErrInvalid) {
		t.Fatal("ideas promote to themes, roadmap or decisions only", err)
	}
}

func TestPinRelationsAndPromoteRoutes(t *testing.T) {
	s, store := ownedFixture(t)
	h := NewHTTP(store, nil)
	h.Owned = s
	a, b := mk(t, s, "themes", "A", nil), mk(t, s, "themes", "B", nil)
	call := func(method, path, body string, hdr ...string) (int, string) {
		req := httptest.NewRequest(method, Prefix+path, strings.NewReader(body))
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	rev := func(r Item) string { return strings.TrimSpace(string(encode(revision(r)))) }
	code, body := call("PUT", "/themes/"+a["id"].(string)+"/pin", `{"pinned":true,"expected_revision":`+rev(a)+`}`)
	if code != 200 || !strings.Contains(body, `"pinned":true`) || !strings.Contains(body, `"pinned_at":"20`) || !strings.Contains(body, `"db":"themes"`) {
		t.Fatal(code, body)
	}
	if code, body = call("PUT", "/themes/"+a["id"].(string)+"/pin", `{"pinned":false,"expected_revision":`+rev(a)+`}`); code != 409 || !strings.Contains(body, `"revision_conflict"`) || !strings.Contains(body, `"current"`) {
		t.Fatal("stale pin", code, body)
	}
	b2 := `{"kind":"needs","to":{"db":"themes","id":"` + a["id"].(string) + `"},"expected_revision":` + rev(b) + `}`
	code, body = call("POST", "/themes/"+b["id"].(string)+"/relations", b2, "X-Planner-Actor", "chrispian")
	if code != 200 || !strings.Contains(body, `"relations_count":1`) || !strings.Contains(body, `"links_view"`) {
		t.Fatal(code, body)
	}
	got, _ := s.Get(context.Background(), "themes", b["id"].(string))
	if by := textField(object(array(got["relations"])[0]), "by"); by != "operator:chrispian" {
		t.Fatal("actor comes from the request", by)
	}
	// a needs b now closes the loop.
	a2, _ := s.Get(context.Background(), "themes", a["id"].(string))
	cyc := `{"kind":"needs","to":{"db":"themes","id":"` + b["id"].(string) + `"},"expected_revision":` + rev(a2) + `}`
	if code, body = call("POST", "/themes/"+a["id"].(string)+"/relations", cyc); code != 422 || !strings.Contains(body, `"cycle"`) {
		t.Fatal(code, body)
	}
	if code, body = call("GET", "/themes/"+b["id"].(string)+"/links", ""); code != 200 || !strings.Contains(body, `"outbound"`) {
		t.Fatal(code, body)
	}
	idea := mk(t, s, "ideas", "I", nil)
	if code, body = call("POST", "/ideas/"+idea["id"].(string)+"/promote", `{"to_db":"decisions","expected_revision":`+rev(idea)+`}`); code != 200 || !strings.Contains(body, `"receipt"`) || !strings.Contains(body, `"promoted"`) {
		t.Fatal(code, body)
	}
	if code, _ = call("POST", "/ideas/"+idea["id"].(string)+"/promote", `{"to_db":"risks","expected_revision":1}`); code != 400 {
		t.Fatal("risks is not a destination", code)
	}
}

func TestRelationIndexIsRebuiltFromTheItemOnEveryWrite(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	a, b := mk(t, s, "themes", "A", nil), mk(t, s, "themes", "B", nil)
	a1, err := s.AddRelation(ctx, PortfolioRef{"themes", a["id"].(string)}, "relates", PortfolioRef{"themes", b["id"].(string)}, revision(a), "operator:test")
	if err != nil {
		t.Fatal(err)
	}
	// An unrelated edit keeps the edge; archiving the source is a tombstone only for deletes, so use SetDeleted.
	if _, err = s.Patch(ctx, "themes", a["id"].(string), revision(a1), Item{"tldr": "changed"}, "operator:test"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.Objects.RelationsFrom(ctx, a["id"].(string)); len(rows) != 1 || rows[0].ToID != b["id"] || rows[0].ToDB != "themes" || rows[0].FromDB != "themes" {
		t.Fatal(rows)
	}
	cur, _ := s.Objects.Get(ctx, a["id"].(string))
	if _, err = s.Objects.SetDeleted(ctx, cur.ID, cur.Revision, true, "operator:test"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.Objects.RelationsTo(ctx, b["id"].(string)); len(rows) != 0 {
		t.Fatal("a deleted source must leave the index", rows)
	}
}
