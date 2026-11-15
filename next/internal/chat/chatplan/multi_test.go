package chatplan

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/next/internal/chat/chatapi"
)

func ptr(s string) *string { return &s }

func TestMultiplePlansOrderAndDelete(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	ref := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: "m1"}
	first := writePlan(t, s, "m1", "One")
	if first.Position != 1 {
		t.Fatalf("existing single plan is plan 1: %d", first.Position)
	}
	two, err := s.CreatePlan(ctx, ref, ptr("Two"), "user")
	if err != nil || two.Position != 2 || two.Revision != 1 {
		t.Fatalf("create: %+v %v", two, err)
	}
	three, _ := s.CreatePlan(ctx, ref, ptr("Three"), "agent")
	if got, _ := s.Plans(ctx, ref); len(got) != 3 || got[0].ID != first.ID || got[2].ID != three.ID {
		t.Fatalf("order: %+v", got)
	}
	// Writing without a plan id still means the first plan.
	if p := writePlanAgain(t, s, ref, first, "One edited"); p.ID != first.ID {
		t.Fatalf("a write without plan_id must hit the first plan: %s", p.ID)
	}
	got, err := s.ReorderPlans(ctx, ref, []string{three.ID, first.ID, two.ID}, "user")
	if err != nil || got[0].ID != three.ID || got[1].Position != 2 {
		t.Fatalf("reorder: %+v %v", got, err)
	}
	if _, err := s.ReorderPlans(ctx, ref, []string{three.ID, first.ID}, "user"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a partial order is invalid: %v", err)
	}
	if _, err := s.ReorderPlans(ctx, ref, []string{three.ID, first.ID, "pl_nope"}, "user"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unknown plan is invalid: %v", err)
	}
	if err := s.DeletePlan(ctx, ref, two.ID, 99, "user"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := s.DeletePlan(ctx, ref, two.ID, two.Revision, "user"); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Plans(ctx, ref); len(left) != 2 || left[1].Position != 2 {
		t.Fatalf("positions renumber after a delete: %+v", left)
	}
	// Another thread's plan cannot be addressed through this thread.
	other := writePlan(t, s, "m2", "Other")
	if err := s.DeletePlan(ctx, ref, other.ID, other.Revision, "user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-thread delete: %v", err)
	}
}

func writePlanAgain(t *testing.T, s *Service, ref chatapi.ThreadRef, cur Plan, title string) Plan {
	t.Helper()
	p, err := s.Write(context.Background(), ref, "thread", WriteIn{By: "agent", Title: ptr(title), HasTitle: true, Expected: cur.Revision, Steps: []StepIn{{ID: cur.Steps[0].ID, Title: ptr(title)}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMoveStepBetweenPlans(t *testing.T) {
	s, events := lineageSetup(t)
	ctx := context.Background()
	ref := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: "mv"}
	a := writePlan(t, s, "mv", "A")
	b, _ := s.CreatePlan(ctx, ref, ptr("B"), "user")
	// link the step to a task: the link and status survive the move
	a2, err := s.Store.LinkStep(ctx, a.ID, a.Steps[0].ID, "CW-1", a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	*events = nil
	from, to, st, err := s.MoveStep(ctx, ref, a.ID, b.ID, a.Steps[0].ID, 0, a2.Revision, b.Revision, "user")
	if err != nil {
		t.Fatal(err)
	}
	if st.ID != a.Steps[0].ID || st.TaskRef == nil || *st.TaskRef != "CW-1" || len(from.Steps) != 0 || len(to.Steps) != 1 || to.Steps[0].Position != 1 {
		t.Fatalf("move: from=%+v to=%+v st=%+v", from, to, st)
	}
	if from.Revision != a2.Revision+1 || to.Revision != b.Revision+1 {
		t.Fatalf("both revisions advance: %d %d", from.Revision, to.Revision)
	}
	if len(*events) < 2 {
		t.Fatalf("each side publishes: %v", *events)
	}
	// stale revisions on either side conflict and change nothing
	if _, _, _, err := s.MoveStep(ctx, ref, b.ID, a.ID, st.ID, 0, b.Revision, from.Revision, "user"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale source: %v", err)
	}
	if _, _, _, err := s.MoveStep(ctx, ref, b.ID, a.ID, st.ID, 0, to.Revision, a.Revision, "user"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale target: %v", err)
	}
	if _, _, _, err := s.MoveStep(ctx, ref, b.ID, b.ID, st.ID, 0, to.Revision, to.Revision, "user"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("same plan: %v", err)
	}
	if _, _, _, err := s.MoveStep(ctx, ref, b.ID, a.ID, "st_nope", 0, to.Revision, from.Revision, "user"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown step: %v", err)
	}
	// moving it back to position 1 of A
	if _, back, _, err := s.MoveStep(ctx, ref, b.ID, a.ID, st.ID, 1, to.Revision, from.Revision, "user"); err != nil || len(back.Steps) != 1 {
		t.Fatalf("move back: %+v %v", back, err)
	}
}

func TestCarryMovesEveryPlan(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	s.Attach(ctx, "c1", "agent-a", "")
	writePlan(t, s, "c1", "First")
	old := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: "c1"}
	if _, err := s.CreatePlan(ctx, old, ptr("Second"), "agent"); err != nil {
		t.Fatal(err)
	}
	s.Attach(ctx, "c2", "agent-a", "c1")
	got, _ := s.Plans(ctx, chatapi.ThreadRef{Source: chatapi.SourceTether, ID: "c2"})
	left, _ := s.Plans(ctx, old)
	if len(got) != 2 || len(left) != 0 || *got[0].Title != "First" || *got[1].Title != "Second" {
		t.Fatalf("all plans carry, in order: %+v / %+v", got, left)
	}
}

func TestProgressSumsPlans(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	ref := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: "pg"}
	writePlan(t, s, "pg", "A")
	b, _ := s.CreatePlan(ctx, ref, ptr("B"), "user")
	if _, _, err := s.Store.AddStep(ctx, b.ID, StepIn{Title: ptr("x")}, b.Revision); err != nil {
		t.Fatal(err)
	}
	if pr := s.Store.Progress(ctx, []string{"tether:pg"})["tether:pg"]; pr.Total != 2 {
		t.Fatalf("progress is summed across plans: %+v", pr)
	}
}

func TestPlansREST(t *testing.T) {
	s, _ := lineageSetup(t)
	h := (&Handler{Service: s}).Mux()
	do := func(method, path, body string) (int, string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/plugins/console/v1/chat"+path, strings.NewReader(body))
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if code, body := do("POST", "/threads/tether:r1/plans", `{"title":"One"}`); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	code, body := do("POST", "/threads/tether:r1/plans", `{"title":"Two"}`)
	var two Plan
	_ = json.Unmarshal([]byte(body), &two)
	if code != 201 || two.Position != 2 {
		t.Fatalf("second: %d %s", code, body)
	}
	var list struct{ Plans []Plan }
	code, body = do("GET", "/threads/tether:r1/plans", "")
	_ = json.Unmarshal([]byte(body), &list)
	if code != 200 || len(list.Plans) != 2 {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, body = do("POST", "/threads/tether:r1/plans/reorder", `{"plan_ids":["`+two.ID+`","`+list.Plans[0].ID+`"]}`); code != 200 {
		t.Fatalf("reorder: %d %s", code, body)
	}
	if code, _ = do("POST", "/threads/tether:r1/plans/reorder", `{}`); code != 400 {
		t.Fatalf("reorder without ids: %d", code)
	}
	if code, _ = do("POST", "/threads/tether:r1/plans/"+two.ID+"/steps/st_x/move", `{"to_plan":"`+list.Plans[0].ID+`","expected_revision":1,"to_expected_revision":1}`); code != 404 {
		t.Fatalf("move of an unknown step: %d", code)
	}
	if code, _ = do("DELETE", "/threads/tether:r1/plans/"+two.ID+"?expected_revision=9", ""); code != 409 {
		t.Fatalf("stale delete: %d", code)
	}
	if code, _ = do("DELETE", "/threads/tether:r1/plans/"+two.ID+"?expected_revision=1", ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
}

func TestPlanScopedStepRoutes(t *testing.T) {
	s, _ := lineageSetup(t)
	ctx := context.Background()
	ref := chatapi.ThreadRef{Source: chatapi.SourceTether, ID: "sr"}
	writePlan(t, s, "sr", "A")
	b, _ := s.CreatePlan(ctx, ref, ptr("B"), "user")
	h := (&Handler{Service: s}).Mux()
	do := func(method, path, body string) (int, string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/api/plugins/console/v1/chat"+path, strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	code, body := do("POST", "/threads/tether:sr/plans/"+b.ID+"/steps", `{"title":"in B","expected_revision":1}`)
	var st Step
	_ = json.Unmarshal([]byte(body), &st)
	if code != 201 || st.ID == "" {
		t.Fatalf("add to plan B: %d %s", code, body)
	}
	if code, body = do("PATCH", "/threads/tether:sr/plans/"+b.ID+"/steps/"+st.ID, `{"status":"done","expected_revision":2}`); code != 200 {
		t.Fatalf("patch in plan B: %d %s", code, body)
	}
	plans, _ := s.Plans(ctx, ref)
	if len(plans[0].Steps) != 1 || len(plans[1].Steps) != 1 || plans[1].Steps[0].Status != "done" {
		t.Fatalf("the step landed in B, A untouched: %+v", plans)
	}
	if code, _ = do("POST", "/threads/tether:sr/plans/pl_nope/steps", `{"title":"x","expected_revision":1}`); code != 404 {
		t.Fatalf("unknown plan: %d", code)
	}
}
