package chatwork

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/chatplan"
	"github.com/hollis-labs/tachyon/internal/plannerassign"
	"github.com/hollis-labs/tachyon/internal/torqueread"
)

func sp(s string) *string { return &s }

func svc() *Service {
	return &Service{
		Plan: func(context.Context, chatapi.ThreadRef) (chatplan.Plan, error) {
			return chatplan.Plan{TaskRef: sp("CW-1"), Status: "approved", Steps: []chatplan.Step{
				{Title: "a", Status: "done", TaskRef: sp("CW-2")},
				{Title: "b", Status: "todo"},
				{Title: "c", Status: "cancelled"},
			}}, nil
		},
		Assigned: func(_ context.Context, agent string) ([]plannerassign.WorkItem, error) {
			return []plannerassign.WorkItem{
				{Kind: "task", ID: "CW-2", Title: "CW-2", UpdatedAt: "2026-10-30T01:00:00Z"},
				{Kind: "decision", ID: "DEC-1", Title: "Pick one", Status: "proposed", UpdatedAt: "2026-10-30T00:00:00Z"},
			}, nil
		},
		Task: func(_ context.Context, id string) (torqueread.Task, error) {
			if id == "CW-9" {
				return torqueread.Task{}, errors.New("down")
			}
			return torqueread.Task{ID: id, Title: "Title " + id, Status: "doing", UpdatedAt: "2026-10-30T02:00:00Z"}, nil
		},
	}
}

func TestWorkJoinsAssignmentsAndPlanLinks(t *testing.T) {
	res, err := svc().Work(context.Background(), chatapi.ThreadRef{Source: chatapi.SourceApp, ID: "t1"}, "agent-x")
	if err != nil {
		t.Fatal(err)
	}
	if !res.AssigneeResolved || res.Source != "planner" || len(res.Items) != 3 {
		t.Fatalf("%+v", res)
	}
	if res.Items[0].ID != "DEC-1" || !res.Items[0].NeedsUser {
		t.Fatalf("decision needing the user sorts first: %+v", res.Items[0])
	}
	by := map[string]Item{}
	for _, it := range res.Items {
		by[it.ID] = it
	}
	if by["CW-2"].Why != "assigned" || by["CW-2"].Title != "Title CW-2" || by["CW-2"].Status != "doing" {
		t.Fatalf("assignment wins and is enriched: %+v", by["CW-2"])
	}
	if p := by["CW-1"].Progress; p == nil || *p != 0.5 {
		t.Fatalf("plan task progress counts non-cancelled steps: %+v", by["CW-1"])
	}
}

func TestWorkWithoutAgentOrSources(t *testing.T) {
	s := svc()
	s.Plan = func(context.Context, chatapi.ThreadRef) (chatplan.Plan, error) {
		return chatplan.Plan{}, chatplan.ErrNotFound
	}
	s.Task = func(context.Context, string) (torqueread.Task, error) { return torqueread.Task{}, errors.New("down") }
	res, _ := s.Work(context.Background(), chatapi.ThreadRef{Source: chatapi.SourceApp, ID: "t1"}, "")
	if len(res.Items) != 0 || res.Items == nil {
		t.Fatalf("%+v", res)
	}
	s.Assigned = func(context.Context, string) ([]plannerassign.WorkItem, error) {
		return []plannerassign.WorkItem{{Kind: "task", ID: "CW-9"}}, nil
	}
	res, _ = s.Work(context.Background(), chatapi.ThreadRef{Source: chatapi.SourceApp, ID: "t1"}, "a")
	if len(res.Items) != 1 || res.Items[0].Title != "CW-9" {
		t.Fatalf("torque failure keeps the row: %+v", res)
	}
}

func TestHandler(t *testing.T) {
	missing := func(context.Context, string) error { return errors.New("no") }
	h := Handler(svc(), missing)
	for path, want := range map[string]int{
		chatapi.Prefix + "/threads/app:t1/work": 404,
		chatapi.Prefix + "/threads/bogus/work":  400,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	h = Handler(svc(), func(context.Context, string) error { return nil })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, chatapi.Prefix+"/threads/app:t1/work?agent=x", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"assignee_resolved":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
