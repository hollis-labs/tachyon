package chatplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const base = "/api/plugins/console/v1/chat"

type env struct {
	svc    *Service
	srv    *httptest.Server
	mu     sync.Mutex
	events []string
	notes  []string
	notify int
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{}
	e.svc = &Service{
		Store: &Store{DB: db},
		Exists: func(_ context.Context, ref string) error {
			if ref == "app:ghost" {
				return errors.New("none")
			}
			return nil
		},
		ProjectOf: func(_ context.Context, ref string) (string, error) {
			if ref == "app:withproj" || ref == "app:withproj2" {
				return "PRJ-1", nil
			}
			return "", nil
		},
		Publish: func(_ context.Context, thread, typ string, f map[string]any) {
			e.mu.Lock()
			e.events = append(e.events, thread+" "+typ)
			e.mu.Unlock()
		},
		Note:   func(_ context.Context, thread string, p Plan) { e.notes = append(e.notes, thread+" "+p.ID) },
		Notify: func(context.Context, string) { e.notify++ },
	}
	e.srv = httptest.NewServer((&Handler{Service: e.svc}).Mux())
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+base+path, strings.NewReader(body))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

func steps(p map[string]any) []map[string]any {
	var out []map[string]any
	for _, v := range p["steps"].([]any) {
		out = append(out, v.(map[string]any))
	}
	return out
}

func TestPlanLifecycleAndRevisions(t *testing.T) {
	e := setup(t)
	if code, _ := e.do(t, "GET", "/threads/app:t1/plan", ""); code != 404 {
		t.Fatalf("no plan yet: %d", code)
	}
	code, p := e.do(t, "PUT", "/threads/app:t1/plan", `{"scope":"thread","title":"Ship it","expected_revision":0,"steps":[{"title":"One","acceptance":"a"},{"title":"Two","status":"doing"},{"title":"Three"}]}`)
	if code != 200 || p["revision"] != 1.0 || p["status"] != "proposed" || p["title"] != "Ship it" || p["created_by"] != "user" || len(steps(p)) != 3 {
		t.Fatal(code, p)
	}
	ss := steps(p)
	if ss[0]["position"] != 1.0 || ss[1]["status"] != "doing" || ss[2]["status"] != "todo" || ss[0]["acceptance"] != "a" {
		t.Fatalf("steps: %v", ss)
	}
	// Stale and duplicate creates are conflicts.
	if code, out := e.do(t, "PUT", "/threads/app:t1/plan", `{"scope":"thread","expected_revision":0,"steps":[]}`); code != 409 || out["code"] != "conflict" {
		t.Fatalf("create over existing: %d %v", code, out)
	}
	// Patch one step with the right revision.
	id1 := ss[0]["id"].(string)
	code, st := e.do(t, "PATCH", "/threads/app:t1/plan/steps/"+id1, `{"status":"done","expected_revision":1}`)
	if code != 200 || st["status"] != "done" || st["plan_revision"] != 2.0 {
		t.Fatal(code, st)
	}
	if code, _ := e.do(t, "PATCH", "/threads/app:t1/plan/steps/"+id1, `{"status":"todo","expected_revision":1}`); code != 409 {
		t.Fatalf("stale step patch: %d", code)
	}
	// Two writers racing on the same revision: exactly one wins.
	id2 := ss[1]["id"].(string)
	var wins, conflicts int
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code, _ := e.do(t, "PATCH", "/threads/app:t1/plan/steps/"+id2, fmt.Sprintf(`{"notes":"n%d","expected_revision":2}`, i))
			mu.Lock()
			defer mu.Unlock()
			if code == 200 {
				wins++
			} else if code == 409 {
				conflicts++
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 || conflicts != 7 {
		t.Fatalf("race: %d wins, %d conflicts", wins, conflicts)
	}
	_, cur := e.do(t, "GET", "/threads/app:t1/plan", "")
	if cur["revision"] != 3.0 {
		t.Fatalf("revision: %v", cur["revision"])
	}
	// Whole-plan rewrite keeps ids, drops omitted steps, adds new ones, renumbers.
	ss = steps(cur)
	id3 := ss[2]["id"].(string)
	body := fmt.Sprintf(`{"scope":"thread","id":%q,"title":null,"expected_revision":3,"steps":[{"id":%q,"title":"Three (edited)"},{"title":"Brand new"},{"id":%q}]}`, cur["id"], id3, id1)
	code, p = e.do(t, "PUT", "/threads/app:t1/plan", body)
	if code != 200 || p["revision"] != 4.0 || p["title"] != nil {
		t.Fatal(code, p)
	}
	ss = steps(p)
	if len(ss) != 3 || ss[0]["id"] != id3 || ss[0]["title"] != "Three (edited)" || ss[2]["id"] != id1 || ss[2]["status"] != "done" || ss[2]["position"] != 3.0 {
		t.Fatalf("rewrite: %v", ss)
	}
	// Add, reorder.
	code, st = e.do(t, "POST", "/threads/app:t1/plan/steps", `{"title":"Late","position":1,"expected_revision":4}`)
	if code != 201 || st["plan_revision"] != 5.0 {
		t.Fatal(code, st)
	}
	_, cur = e.do(t, "GET", "/threads/app:t1/plan", "")
	ss = steps(cur)
	if ss[0]["title"] != "Late" || len(ss) != 4 {
		t.Fatalf("insert at 1: %v", ss)
	}
	var ids []string
	for i := len(ss) - 1; i >= 0; i-- {
		ids = append(ids, ss[i]["id"].(string))
	}
	b, _ := json.Marshal(map[string]any{"step_ids": ids, "expected_revision": 5})
	code, p = e.do(t, "POST", "/threads/app:t1/plan/reorder", string(b))
	if code != 200 || steps(p)[0]["id"] != ids[0] || p["revision"] != 6.0 {
		t.Fatal(code, p)
	}
	b, _ = json.Marshal(map[string]any{"step_ids": ids[:3], "expected_revision": 6})
	if code, _ := e.do(t, "POST", "/threads/app:t1/plan/reorder", string(b)); code != 400 {
		t.Fatalf("partial reorder: %d", code)
	}
	b, _ = json.Marshal(map[string]any{"step_ids": []string{ids[0], ids[0], ids[1], ids[2]}, "expected_revision": 6})
	if code, _ := e.do(t, "POST", "/threads/app:t1/plan/reorder", string(b)); code != 400 {
		t.Fatalf("duplicate reorder: %d", code)
	}
	// A different thread has its own plan.
	if code, _ := e.do(t, "GET", "/threads/app:t2/plan", ""); code != 404 {
		t.Fatalf("other thread: %d", code)
	}
	// Events: plan on every write; a user write sends no panel signal and no plan message.
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range e.events {
		if ev != "app:t1 plan" {
			t.Fatalf("unexpected event %q", ev)
		}
	}
	if len(e.notes) != 0 {
		t.Fatalf("user write appended plan messages: %v", e.notes)
	}
}

func TestValidationAndScopes(t *testing.T) {
	e := setup(t)
	for name, body := range map[string]string{
		"no scope":    `{"expected_revision":0,"steps":[]}`,
		"no expected": `{"scope":"thread","steps":[]}`,
		"neg":         `{"scope":"thread","expected_revision":-1,"steps":[]}`,
		"unknown":     `{"scope":"thread","expected_revision":0,"steps":[],"z":1}`,
		"no title":    `{"scope":"thread","expected_revision":0,"steps":[{"title":" "}]}`,
		"bad status":  `{"scope":"thread","expected_revision":0,"steps":[{"title":"a","status":"nope"}]}`,
		"plan status": `{"scope":"thread","expected_revision":0,"status":"nope","steps":[]}`,
		"unknown id":  `{"scope":"thread","expected_revision":0,"steps":[{"id":"st_x","title":"a"}]}`,
		"long title":  `{"scope":"thread","expected_revision":0,"steps":[{"title":"` + strings.Repeat("a", 201) + `"}]}`,
	} {
		if code, _ := e.do(t, "PUT", "/threads/app:t1/plan", body); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	var many []string
	for i := 0; i < MaxSteps+1; i++ {
		many = append(many, `{"title":"s"}`)
	}
	if code, _ := e.do(t, "PUT", "/threads/app:t1/plan", `{"scope":"thread","expected_revision":0,"steps":[`+strings.Join(many, ",")+`]}`); code != 400 {
		t.Errorf("101 steps: %d", code)
	}
	if code, _ := e.do(t, "PUT", "/threads/app:ghost/plan", `{"scope":"thread","expected_revision":0,"steps":[]}`); code != 404 {
		t.Errorf("ghost thread: %d", code)
	}
	if code, _ := e.do(t, "GET", "/threads/app:t1/plan?scope=weird", ""); code != 400 {
		t.Errorf("bad scope: %d", code)
	}
	// Project scope needs a project; two chats of one project share the plan.
	if code, out := e.do(t, "PUT", "/threads/app:t1/plan", `{"scope":"project","expected_revision":0,"steps":[]}`); code != 409 || out["code"] != "no_project" {
		t.Errorf("no project: %d %v", code, out)
	}
	code, p := e.do(t, "PUT", "/threads/app:withproj/plan", `{"scope":"project","title":"Roadmap","expected_revision":0,"steps":[{"title":"Plan the project"}]}`)
	if code != 200 || p["scope"] != "project" || p["project_id"] != "PRJ-1" || p["thread_ref"] != nil {
		t.Fatal(code, p)
	}
	if code, p = e.do(t, "GET", "/threads/app:withproj2/plan?scope=project", ""); code != 200 || p["title"] != "Roadmap" {
		t.Fatalf("shared project plan: %d %v", code, p)
	}
	// A thread plan and the project plan are distinct.
	if code, _ = e.do(t, "GET", "/threads/app:withproj/plan", ""); code != 404 {
		t.Fatalf("thread plan leaked from project plan: %d", code)
	}
	// An explicit id must name the plan being replaced.
	if code, _ = e.do(t, "PUT", "/threads/app:withproj/plan", `{"scope":"project","id":"pl_other","expected_revision":1,"steps":[]}`); code != 409 {
		t.Errorf("wrong plan id: %d", code)
	}
}

func TestLinkedStepsFollowTorque(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, p := e.do(t, "PUT", "/threads/app:t1/plan", `{"scope":"thread","expected_revision":0,"steps":[{"title":"Linked"},{"title":"Free"}]}`)
	ss := steps(p)
	linked := ss[0]["id"].(string)
	if _, err := e.svc.Store.LinkStep(ctx, p["id"].(string), linked, "CW-1", 1); err != nil {
		t.Fatal(err)
	}
	// By hand a linked step may only be parked and unparked.
	if code, out := e.do(t, "PATCH", "/threads/app:t1/plan/steps/"+linked, `{"status":"done","expected_revision":2}`); code != 409 || out["code"] != "linked_step" {
		t.Fatalf("done by hand: %d %v", code, out)
	}
	if code, _ := e.do(t, "PATCH", "/threads/app:t1/plan/steps/"+linked, `{"status":"blocked","expected_revision":2}`); code != 200 {
		t.Fatal("park refused")
	}
	if code, _ := e.do(t, "PATCH", "/threads/app:t1/plan/steps/"+linked, `{"status":"todo","expected_revision":3}`); code != 200 {
		t.Fatal("unpark refused")
	}
	// A whole-plan write cannot move a linked step's status or its task link.
	_, cur := e.do(t, "GET", "/threads/app:t1/plan", "")
	body := fmt.Sprintf(`{"scope":"thread","expected_revision":%v,"steps":[{"id":%q,"title":"Linked","status":"done"},{"title":"Free"}]}`, cur["revision"], linked)
	if code, _ := e.do(t, "PUT", "/threads/app:t1/plan", body); code != 409 {
		t.Fatalf("whole-plan status on a linked step: %d", code)
	}
	body = fmt.Sprintf(`{"scope":"thread","expected_revision":%v,"steps":[{"id":%q,"title":"Linked, renamed"}]}`, cur["revision"], linked)
	code, out := e.do(t, "PUT", "/threads/app:t1/plan", body)
	if code != 200 || steps(out)[0]["task_ref"] != "CW-1" {
		t.Fatalf("task link lost on rewrite: %d %v", code, out)
	}
	// The one-way sync.
	src := func(_ context.Context, refs []string) (map[string]string, error) {
		if len(refs) != 1 || refs[0] != "CW-1" {
			t.Fatalf("refs: %v", refs)
		}
		return map[string]string{"CW-1": "review", "CW-9": "done"}, nil
	}
	changed, err := e.svc.Store.SyncTaskStatuses(ctx, src)
	if err != nil || len(changed) != 1 {
		t.Fatal(changed, err)
	}
	_, after := e.do(t, "GET", "/threads/app:t1/plan", "")
	if steps(after)[0]["status"] != "doing" || after["revision"] != out["revision"].(float64)+1 {
		t.Fatalf("sync: %v", after)
	}
	if changed, _ = e.svc.Store.SyncTaskStatuses(ctx, src); len(changed) != 0 { // nothing differs now
		t.Fatalf("second sync changed %v", changed)
	}
	if _, err := e.svc.Store.SyncTaskStatuses(ctx, func(context.Context, []string) (map[string]string, error) { return nil, errors.New("down") }); err == nil {
		t.Fatal("source error not returned")
	}
	for torque, want := range map[string]string{"backlog": "todo", "paused": "blocked", "done": "done", "archived": "cancelled", "abandoned": "cancelled"} {
		if got, ok := StepStatusFromTorque(torque); !ok || got != want {
			t.Errorf("%s -> %s %v", torque, got, ok)
		}
	}
	if _, ok := StepStatusFromTorque("mystery"); ok {
		t.Error("unknown status mapped")
	}
}

func TestAgentWritesProposeNeverApprove(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	ref := chatapi.ThreadRef{Source: "app", ID: "t1"}
	title := "Agent plan"
	st := func(s string) StepIn { return StepIn{Title: &s} }
	p, err := e.svc.Write(ctx, ref, "thread", WriteIn{By: "agent", Title: &title, HasTitle: true, Status: "approved", Steps: []StepIn{st("a"), st("b")}})
	if err != nil || p.Status != "proposed" || p.CreatedBy != "agent" {
		t.Fatal(p, err)
	}
	if len(e.notes) != 1 || e.notes[0] != "app:t1 "+p.ID {
		t.Fatalf("plan message: %v", e.notes)
	}
	// The operator approves; a later agent rewrite keeps that status.
	if _, err := e.svc.Store.SetStatus(ctx, p.ID, "approved", 1); err != nil {
		t.Fatal(err)
	}
	p2, err := e.svc.Write(ctx, ref, "thread", WriteIn{By: "agent", Expected: 2, Status: "active", Steps: []StepIn{{ID: p.Steps[0].ID}, st("c")}})
	if err != nil || p2.Status != "approved" {
		t.Fatalf("agent changed approval: %v %v", p2.Status, err)
	}
	// An agent can cancel its own plan.
	if p3, err := e.svc.Write(ctx, ref, "thread", WriteIn{By: "agent", Expected: p2.Revision, Status: "cancelled", Steps: []StepIn{{ID: p2.Steps[0].ID}}}); err != nil || p3.Status != "cancelled" {
		t.Fatal(p3.Status, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	panels := 0
	for _, ev := range e.events {
		if ev == "app:t1 panel" {
			panels++
		}
	}
	if panels != 3 || e.notify != 3 {
		t.Fatalf("panel signals %d, notifies %d", panels, e.notify)
	}
}

func TestProgressAndPurge(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.do(t, "PUT", "/threads/app:t1/plan", `{"scope":"thread","expected_revision":0,"steps":[{"title":"a","status":"done"},{"title":"b"},{"title":"c","status":"cancelled"}]}`)
	e.do(t, "PUT", "/threads/app:t2/plan", `{"scope":"thread","status":"cancelled","expected_revision":0,"steps":[{"title":"x"}]}`)
	e.do(t, "PUT", "/threads/app:t3/plan", `{"scope":"thread","expected_revision":0,"steps":[]}`)
	got := e.svc.Store.Progress(ctx, nil)
	if got["app:t1"] != (Progress{Done: 1, Total: 2}) {
		t.Fatalf("t1: %v", got)
	}
	if _, ok := got["app:t2"]; ok {
		t.Fatal("a cancelled plan reports progress")
	}
	if got["app:t3"] != (Progress{}) {
		t.Fatalf("empty plan: %v", got["app:t3"])
	}
	if _, ok := got["app:t3"]; !ok {
		t.Fatal("an empty plan still marks the thread as having a plan")
	}
	if err := e.svc.Store.PurgeThread(ctx, "app:t1"); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.do(t, "GET", "/threads/app:t1/plan", ""); code != 404 {
		t.Fatalf("purged: %d", code)
	}
	var n int
	_ = e.svc.Store.DB.Reader.QueryRow(`SELECT COUNT(*) FROM chat_plan_steps WHERE plan_id NOT IN (SELECT id FROM chat_plans)`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d orphan steps", n)
	}
}

func TestWritesRefusedWithoutOperatorSession(t *testing.T) {
	e := setup(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer up.Close()
	guarded := approvals.New(up.URL).RequireOperator((&Handler{Service: e.svc}).Mux())
	call := func(method, path, origin string, cookie bool) int {
		r := httptest.NewRequest(method, "https://tachyon.test"+base+path, strings.NewReader(`{"scope":"thread","expected_revision":0,"steps":[]}`))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie {
			r.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "t"})
		}
		w := httptest.NewRecorder()
		guarded.ServeHTTP(w, r)
		return w.Code
	}
	for _, w := range [][2]string{{"PUT", "/threads/app:t1/plan"}, {"POST", "/threads/app:t1/plan/steps"}, {"PATCH", "/threads/app:t1/plan/steps/x"}, {"POST", "/threads/app:t1/plan/reorder"}} {
		if c := call(w[0], w[1], "https://tachyon.test", false); c != 401 && c != 403 {
			t.Errorf("%v without session: %d", w, c)
		}
		if c := call(w[0], w[1], "https://evil.test", true); c != 403 {
			t.Errorf("%v cross-origin: %d", w, c)
		}
		if c := call(w[0], w[1], "", true); c != 403 {
			t.Errorf("%v no origin: %d", w, c)
		}
	}
	if c := call("GET", "/threads/app:t1/plan", "", false); c != 404 { // open read: reaches the handler
		t.Errorf("open read: %d", c)
	}
}
