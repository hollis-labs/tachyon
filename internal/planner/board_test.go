package planner

import (
	"context"
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
