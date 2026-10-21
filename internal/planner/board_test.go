package planner

import (
	"context"
	"github.com/hollis-labs/tachyon/internal/objects"
	"testing"

	"github.com/google/uuid"
)

func boardRows(t *testing.T, s *Owned) map[string][]Item {
	t.Helper()
	out, err := s.Board(context.Background(), nil, BoardOptions{Scope: "curated"})
	if err != nil {
		t.Fatal(err)
	}
	return out["sections"].(map[string][]Item)
}

func boardTitles(rows []Item) []string {
	titles := []string{}
	for _, r := range rows {
		titles = append(titles, textField(r, "title"))
	}
	return titles
}

func equalTitles(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCuratedBoardRanksByOrderThenPriorityAndKeepsRankOnEdit(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	ids := map[string]string{}
	add := func(title string, f Item) {
		t.Helper()
		id := uuid.NewString()
		f["title"], f["status"], f["tags"] = title, "active", []any{"board"}
		if _, err := s.Create(ctx, "workstreams", id, f, "test"); err != nil {
			t.Fatal(err)
		}
		ids[title] = id
	}
	add("unranked low", Item{"priority": 3})
	add("unranked high", Item{"priority": 1})
	add("unset priority", Item{})
	add("ordered second", Item{"order": 20, "priority": 5})
	add("ordered first", Item{"order": 10})
	add("blocked and ordered third", Item{"order": 30, "blockers": []any{"waiting on a decision"}})

	want := []string{"ordered first", "ordered second", "blocked and ordered third", "unranked high", "unranked low", "unset priority"}
	if got := boardTitles(boardRows(t, s)["in_flight"]); !equalTitles(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	// Editing a row bumps its updated time but must not change its rank.
	row, err := s.Get(ctx, "workstreams", ids["ordered second"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Patch(ctx, "workstreams", ids["ordered second"], revision(row), Item{"tldr": "edited"}, "test"); err != nil {
		t.Fatal(err)
	}
	if got := boardTitles(boardRows(t, s)["in_flight"]); !equalTitles(got, want) {
		t.Fatalf("edit changed rank: got %v want %v", got, want)
	}

	// A deliberate reorder does change it.
	changes := []OrderChange{}
	for _, title := range []string{"ordered second", "ordered first"} {
		r, err := s.Get(ctx, "workstreams", ids[title])
		if err != nil {
			t.Fatal(err)
		}
		changes = append(changes, OrderChange{ID: ids[title], ExpectedRevision: revision(r)})
	}
	if _, err := s.Reorder(ctx, "workstreams", changes, "test"); err != nil {
		t.Fatal(err)
	}
	want = []string{"ordered second", "ordered first", "blocked and ordered third", "unranked high", "unranked low", "unset priority"}
	if got := boardTitles(boardRows(t, s)["in_flight"]); !equalTitles(got, want) {
		t.Fatalf("reorder: got %v want %v", got, want)
	}
}

func TestBoardRowAppearsInOneSectionOnly(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	for _, ws := range []Item{
		{"title": "active blocked", "status": "active", "blockers": []any{"x"}},
		{"title": "paused blocked", "status": "paused", "blockers": []any{"y"}},
		{"title": "draft blocked", "status": "draft", "blockers": []any{"z"}},
	} {
		ws["tags"] = []any{"board"}
		if _, err := s.Create(ctx, "workstreams", uuid.NewString(), ws, "test"); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]string{}
	for section, rows := range boardRows(t, s) {
		for _, r := range rows {
			id := textField(r, "id")
			if prev, dup := seen[id]; dup {
				t.Fatalf("%s appears in %s and %s", textField(r, "title"), prev, section)
			}
			seen[id] = section
			if rows := array(r["blockers"]); len(rows) != 1 {
				t.Fatalf("%s lost its blockers", textField(r, "title"))
			}
		}
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(seen))
	}
}

func TestThemesIsTheCanonicalNameForPriorities(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	row, err := s.Create(ctx, "themes", id, Item{"title": "Managed agent capacity"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if row["status"] != "proposed" {
		t.Fatalf("new theme should start proposed, got %v", row["status"])
	}
	// The same record is reachable under both names.
	for _, db := range []string{"themes", "priorities"} {
		got, err := s.Get(ctx, db, id)
		if err != nil || got["id"] != id {
			t.Fatal(db, got, err)
		}
		listed, err := s.List(ctx, db, nil, "")
		if err != nil || len(listed) != 1 {
			t.Fatal(db, listed, err)
		}
	}
	schema, err := Schema("themes")
	if err != nil {
		t.Fatal(err)
	}
	if schema["db"] != "themes" || schema["id"] != "tachyon/planner/themes@1" {
		t.Fatal("schema should be published under the public name", schema["db"], schema["id"])
	}
	enum, _ := object(object(schema["fields"])["status"])["enum"].([]string)
	if !equalTitles(enum, themeStatuses) {
		t.Fatalf("schema advertises %v", enum)
	}
	for _, k := range []string{"depends_on", "tasks", "decision_ids", "risk_ids", "accountable", "executing_owner", "last_verified", "approved_by", "approved_at"} {
		if _, ok := object(schema["fields"])[k]; !ok {
			t.Fatalf("schema lacks %s", k)
		}
	}
	if _, err := Schema("priorities"); err != nil {
		t.Fatal("the old name must keep working", err)
	}
}

func TestThemeStatusesAndApprovalFieldsAreProtected(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	// An unmigrated record keeps its earlier status and stays writable.
	legacy := seedLegacyTheme(t, s, id, "Legacy", "active")
	if legacy["status"] != "active" {
		t.Fatal(legacy)
	}
	// New writes cannot use the retired statuses, but editing a legacy record
	// that keeps its status still works.
	if _, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "Retired", "status": "active"}, "test"); err == nil {
		t.Fatal("create accepted a retired status")
	}
	if _, err := s.Patch(ctx, "themes", id, revision(legacy), Item{"status": "draft"}, "test"); err == nil {
		t.Fatal("patch moved a theme to a retired status")
	}
	if _, err := s.Patch(ctx, "themes", id, revision(legacy), Item{"summary": "edited"}, "test"); err != nil {
		t.Fatal("a legacy record must stay editable", err)
	}
	legacy = View(mustResolve(t, s, id))
	if _, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "Bad", "status": "queued"}, "test"); err == nil {
		t.Fatal("queued is no longer a theme status")
	}
	for _, f := range []string{"approved_by", "approved_at"} {
		if _, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "Forged", f: "chrispian"}, "agent"); err == nil {
			t.Fatalf("create accepted %s", f)
		}
		if _, err := s.Patch(ctx, "themes", id, revision(legacy), Item{f: "chrispian"}, "agent"); err == nil {
			t.Fatalf("patch accepted %s", f)
		}
	}
	// Other databases are unaffected.
	if _, err := s.Create(ctx, "workstreams", uuid.NewString(), Item{"title": "ws", "approved_by": "x"}, "test"); err != nil {
		t.Fatal("approval fields are only reserved on themes", err)
	}
}

// seedLegacyTheme stores a theme with a retired status the way an unmigrated
// record exists, bypassing the write guard that now refuses it.
func seedLegacyTheme(t *testing.T, s *Owned, id, title, status string, extra ...Item) Item {
	t.Helper()
	row := Item{"title": title, "status": status, "author": "test"}
	for _, e := range extra {
		for k, v := range e {
			row[k] = v
		}
	}
	b, err := definition("priorities", row, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Objects.CreateWithID(context.Background(), id, rawDefinition{databaseKinds["priorities"], b}, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	return View(r)
}

func mustResolve(t *testing.T, s *Owned, id string) objects.Record {
	t.Helper()
	r, err := s.Resolve(context.Background(), "themes", id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
