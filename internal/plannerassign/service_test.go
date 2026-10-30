package plannerassign

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
)

type fix struct {
	s            *Service
	agent, theme string
	team         string
}

func setup(t *testing.T) fix {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	store, err := localstore.Open(ctx, filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	o := objects.New(store)
	s := New(o, "")
	a, err := o.Create(ctx, objects.Agent{Common: objects.Common{Name: "Tachyon PM"}, Enabled: true}, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	tm, err := o.Create(ctx, objects.Team{Common: objects.Common{Name: "Settings team"}}, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := s.Owned.Create(ctx, "themes", id, planner.Item{"title": "Agents console", "status": "proposed"}, "test"); err != nil {
		t.Fatal(err)
	}
	return fix{s: s, agent: a.ID, team: tm.ID, theme: id}
}

func TestCreateListFilterEndAndPrimary(t *testing.T) {
	f, ctx := setup(t), context.Background()
	a, err := f.s.Create(ctx, Create{Target: Target{DB: "themes", ID: f.theme}, AgentID: f.agent, Note: "owns it"}, Actor)
	if err != nil || a.State != StateActive || a.AgentID != f.agent || a.Label != "Tachyon PM" || a.Target.DB != "themes" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := f.s.Create(ctx, Create{Target: Target{Kind: "torque.task", Ref: "CW-20261030-0202"}, TeamID: f.team}, Actor); err != nil {
		t.Fatal(err)
	}
	second, err := f.s.Create(ctx, Create{Target: Target{DB: "themes", ID: f.theme}, TeamID: f.team}, Actor)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		f    Filter
		want int
	}{
		"all":         {Filter{}, 3},
		"agent":       {Filter{Agent: f.agent}, 1},
		"theme":       {Filter{Target: "themes/" + f.theme}, 2},
		"task":        {Filter{Target: "CW-20261030-0202"}, 1},
		"team":        {Filter{Agent: f.team}, 2},
		"unknown":     {Filter{Agent: "nobody"}, 0},
		"unknown_tgt": {Filter{Target: "themes/" + uuid.NewString()}, 0},
	} {
		got, err := f.s.List(ctx, c.f)
		if err != nil || len(got) != c.want {
			t.Errorf("%s: %d want %d (%v)", name, len(got), c.want, err)
		}
	}
	as, _ := f.s.Assignees(ctx, f.theme)
	if len(as) != 2 || !as[0].Primary || as[1].Primary || as[0].ID != a.ID {
		t.Errorf("assignees: %+v", as)
	}
	// planner.list already filters by assigned_to through the same links.
	rows, err := f.s.Objects.ListPlanner(ctx, objects.PlannerFilter{Kind: objects.StrategicPriorityKind, AssignedTo: f.agent})
	if err != nil || len(rows) != 1 || rows[0].ID != f.theme {
		t.Errorf("planner list assigned_to: %v %v", rows, err)
	}
	done, err := f.s.End(ctx, second.ID, StateCompleted, Actor)
	if err != nil || done.State != StateCompleted || done.EndedAt == "" {
		t.Fatalf("%+v %v", done, err)
	}
	if _, err := f.s.End(ctx, second.ID, StateCancelled, Actor); err != objects.ErrConflict {
		t.Errorf("end twice: %v", err)
	}
	if _, err := f.s.End(ctx, f.theme, StateCancelled, Actor); err != objects.ErrNotFound {
		t.Errorf("not an assignment: %v", err)
	}
	if as, _ := f.s.Assignees(ctx, f.theme); len(as) != 1 {
		t.Errorf("after end: %d", len(as))
	}
	if got, _ := f.s.List(ctx, Filter{State: StateCompleted}); len(got) != 1 {
		t.Errorf("completed filter: %d", len(got))
	}
}

func TestCreateValidation(t *testing.T) {
	f, ctx := setup(t), context.Background()
	theme := Target{DB: "themes", ID: f.theme}
	for name, c := range map[string]Create{
		"both":            {Target: theme, AgentID: f.agent, TeamID: f.team},
		"neither":         {Target: theme},
		"no target":       {AgentID: f.agent},
		"mixed target":    {Target: Target{DB: "themes", ID: f.theme, Ref: "x"}, AgentID: f.agent},
		"long note":       {Target: theme, AgentID: f.agent, Note: strings.Repeat("a", MaxNote+1)},
		"bad db":          {Target: Target{DB: "nope", ID: f.theme}, AgentID: f.agent},
		"missing item":    {Target: Target{DB: "themes", ID: uuid.NewString()}, AgentID: f.agent},
		"agent is a team": {Target: theme, AgentID: f.team},
		"unknown agent":   {Target: theme, AgentID: "ghost"},
	} {
		if _, err := f.s.Create(ctx, c, Actor); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func call(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, "http://x"+planner.Prefix+path, strings.NewReader(body)))
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func TestHTTPAndWork(t *testing.T) {
	f := setup(t)
	h := f.s.Handler()
	code, m := call(t, h, "POST", "/assignments", `{"target":{"db":"themes","id":"`+f.theme+`"},"agent_id":"`+f.agent+`"}`)
	if code != 201 || m["state"] != "active" {
		t.Fatalf("create: %d %v", code, m)
	}
	id := m["id"].(string)
	if code, _ := call(t, h, "POST", "/assignments", `{"target":{"db":"themes","id":"x"},"agent_id":"ghost","extra":1}`); code != 400 {
		t.Errorf("unknown field: %d", code)
	}
	if code, _ := call(t, h, "POST", "/assignments", `{"target":{"db":"themes","id":"`+f.theme+`"},"agent_id":"ghost"}`); code != 404 {
		t.Errorf("unknown agent: %d", code)
	}
	call(t, h, "POST", "/assignments", `{"target":{"kind":"torque.task","ref":"CW-20261030-0202"},"agent_id":"`+f.agent+`"}`)
	code, m = call(t, h, "GET", "/agents/"+f.agent+"/work", "")
	items := m["items"].([]any)
	if code != 200 || len(items) != 2 {
		t.Fatalf("work: %d %v", code, m)
	}
	kinds := map[string]bool{}
	for _, it := range items {
		o := it.(map[string]any)
		kinds[o["kind"].(string)] = true
		if o["why"] != "assigned" {
			t.Errorf("why: %v", o["why"])
		}
	}
	if !kinds["theme"] || !kinds["task"] {
		t.Errorf("kinds: %v", kinds)
	}
	if code, _ := call(t, h, "GET", "/assignments?bogus=1", ""); code != 400 {
		t.Errorf("unknown param: %d", code)
	}
	if code, _ := call(t, h, "GET", "/assignments?state=nope", ""); code != 400 {
		t.Errorf("bad state: %d", code)
	}
	if code, m := call(t, h, "POST", "/assignments/"+id+"/cancel", ""); code != 200 || m["state"] != "cancelled" {
		t.Errorf("cancel: %d %v", code, m)
	}
	if code, _ := call(t, h, "POST", "/assignments/"+id+"/complete", ""); code != 409 {
		t.Errorf("complete ended: %d", code)
	}
	if code, _ := call(t, h, "POST", "/assignments/"+uuid.NewString()+"/cancel", ""); code != 404 {
		t.Errorf("unknown id: %d", code)
	}
	if _, m = call(t, h, "GET", "/agents/"+f.agent+"/work", ""); len(m["items"].([]any)) != 1 {
		t.Errorf("work after cancel: %v", m)
	}
	if _, m = call(t, h, "GET", "/agents/nobody/work", ""); len(m["items"].([]any)) != 0 {
		t.Errorf("work for nobody: %v", m)
	}
}

func TestAssignUnassignAndListFilter(t *testing.T) {
	f, ctx := setup(t), context.Background()
	theme := Target{DB: "themes", ID: f.theme}
	a, err := f.s.Assign(ctx, Create{Target: theme, AgentID: f.agent}, Actor)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.s.Assign(ctx, Create{Target: theme, AgentID: f.agent}, Actor)
	if err != nil || again.ID != a.ID {
		t.Fatalf("assign must be repeat-safe: %+v %v", again, err)
	}
	if all, _ := f.s.List(ctx, Filter{}); len(all) != 1 {
		t.Fatalf("one assignment expected: %d", len(all))
	}
	rows, err := f.s.Owned.List(ctx, "themes", map[string]string{"assigned_to": f.agent}, "")
	if err != nil || len(rows) != 1 || rows[0]["id"] != f.theme {
		t.Fatalf("assigned_to filter: %v %v", rows, err)
	}
	if rows, _ := f.s.Owned.List(ctx, "themes", map[string]string{"assigned_to": "nobody"}, ""); len(rows) != 0 {
		t.Fatalf("unknown assignee matches nothing: %v", rows)
	}
	ended, err := f.s.Unassign(ctx, Create{Target: theme, AgentID: f.agent}, Actor)
	if err != nil || len(ended) != 1 || ended[0].State != StateCancelled {
		t.Fatalf("unassign: %+v %v", ended, err)
	}
	if rows, _ := f.s.Owned.List(ctx, "themes", map[string]string{"assigned_to": f.agent}, ""); len(rows) != 0 {
		t.Fatalf("ended assignment no longer filters: %v", rows)
	}
	if none, err := f.s.Unassign(ctx, Create{Target: theme, AgentID: f.agent}, Actor); err != nil || len(none) != 0 {
		t.Fatalf("unassign with nothing active: %v %v", none, err)
	}
}
