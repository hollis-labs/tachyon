package planner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type torqueFake struct {
	mu     sync.Mutex
	status map[string]string
	posts  []string
}

func newTorqueFake(t *testing.T) (*torqueFake, *httptest.Server) {
	f := &torqueFake{status: map[string]string{"CW-1": "doing", "CW-2": "done", "CW-3": "todo", "CW-4": "blocked", "CW-5": "cancelled"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/tasks":
			items := []map[string]any{}
			for _, id := range []string{"CW-1", "CW-2", "CW-3", "CW-4", "CW-5"} {
				items = append(items, map[string]any{"id": id, "title": "Task " + id + " usage", "status": f.status[id], "priority": 2, "updated_at": "2026-10-30T0" + id[3:] + ":00:00Z"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "meta": map[string]any{"has_more": false}})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/tasks/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
			st, ok := f.status[id]
			if !ok {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "title": "T", "status": st, "priority": 3, "blocked_reason": "waiting"})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/transition"):
			b, _ := io.ReadAll(r.Body)
			f.posts = append(f.posts, r.URL.Path+" "+string(b))
			var body struct { // like Torque's REST handler: status and force only
				Status string `json:"status"`
				Force  bool   `json:"force"`
			}
			_ = json.Unmarshal(b, &body)
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/"), "/transition")
			if f.status[id] == "done" && !body.Force {
				w.WriteHeader(409)
				return
			}
			f.status[id] = body.Status
			w.WriteHeader(200)
		case r.Method == "POST" && r.URL.Path == "/api/v1/tasks":
			b, _ := io.ReadAll(r.Body)
			f.posts = append(f.posts, r.URL.Path+" "+string(b))
			var in struct{ Title string }
			_ = json.Unmarshal(b, &in)
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "CW-9", "title": in.Title, "status": "todo"})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/comments"):
			b, _ := io.ReadAll(r.Body)
			f.posts = append(f.posts, r.URL.Path+" "+string(b))
			w.WriteHeader(201)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func taskHTTP(t *testing.T) (*HTTP, *torqueFake) {
	s, store := ownedFixture(t)
	f, srv := newTorqueFake(t)
	h := NewHTTP(store, NewTasks(srv.URL, ""))
	h.Owned = s
	return h, f
}

func (h *HTTP) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, Prefix+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestThemeTasksGroupedByLaneWithMetaAndDetectedCites(t *testing.T) {
	h, _ := taskHTTP(t)
	ctx := context.Background()
	ws := mk(t, h.Owned, "workstreams", "WS", Item{"torque_tag": "planner"})
	th := mk(t, h.Owned, "themes", "Theme", Item{"workstream_ids": []any{ws["id"]}})
	if code, _ := h.do(t, "PUT", "/tasks/CW-2/meta", `{"done_summary":"History list.","pr_cites":["hollis-labs/tachyon#224"]}`); code != 200 {
		t.Fatal("meta", code)
	}
	code, out := h.do(t, "GET", "/themes/"+th["id"].(string)+"/tasks", "")
	if code != 200 {
		t.Fatal(code, out)
	}
	counts := object(out["counts"])
	if number(counts["done"]) != 1 || number(counts["doing"]) != 1 || number(counts["todo"]) != 1 || number(counts["blocked"]) != 1 || number(counts["total"]) != 4 {
		t.Fatal("cancelled tasks have no lane; counts:", counts)
	}
	done := array(object(out["lanes"])["done"])
	card := object(done[0])
	if card["done_summary"] != "History list." || card["priority"] != "high" || len(array(card["pr_cites"])) != 1 || object(array(card["pr_cites"])[0])["source"] != "manual" {
		t.Fatal(card)
	}
	_ = ctx
}

func TestTransitionRulesConflictUndoAndForce(t *testing.T) {
	h, f := taskHTTP(t)
	code, out := h.do(t, "POST", "/tasks/CW-1/transition", `{"to":"review","expected_status":"doing"}`)
	if code != 200 || object(out["task"])["previous"] != "doing" || object(out["undo"])["to"] != "doing" || object(out["undo"])["expected_status"] != "review" || number(object(out["undo"])["window_seconds"]) != 8 {
		t.Fatal(code, out)
	}
	delta := object(object(out["event"])["delta"])
	if number(delta["doing"]) != -1 || number(delta["review"]) != 1 {
		t.Fatal(delta)
	}
	if code, out = h.do(t, "POST", "/tasks/CW-1/transition", `{"to":"done","expected_status":"doing"}`); code != 409 || object(object(out["details"])["current"])["status"] != "review" {
		t.Fatal("stale expected_status", code, out)
	}
	if code, out = h.do(t, "POST", "/tasks/CW-1/transition", `{"to":"blocked"}`); code != 400 || object(out["details"])["field"] != "reason" {
		t.Fatal("blocked needs a reason", code, out)
	}
	if code, _ = h.do(t, "POST", "/tasks/CW-1/transition", `{"to":"review"}`); code != 200 || len(f.posts) != 1 {
		t.Fatal("same status is a no-op", code, f.posts)
	}
	if code, out = h.do(t, "POST", "/tasks/CW-2/transition", `{"to":"doing","expected_status":"done"}`); code != 200 || f.status["CW-2"] != "doing" {
		t.Fatal("leaving done sends force; the comment goes to /comments, not the transition", code, out, f.posts)
	}
	if code, _ = h.do(t, "POST", "/tasks/CW-3/transition", `{"to":"archived"}`); code != 400 {
		t.Fatal("to must be a lane", code)
	}
	code, out = h.do(t, "POST", "/tasks/CW-3/transition", `{"to":"done","summary":"Shipped it.","pr":"hollis-labs/tachyon#9"}`)
	if code != 200 || !strings.Contains(f.posts[len(f.posts)-1], "Done: Shipped it.") {
		t.Fatal(code, out, f.posts)
	}
	_, meta := h.do(t, "PUT", "/tasks/CW-3/meta", `{}`)
	if meta["done_summary"] != "Shipped it." || len(array(meta["pr_cites"])) != 1 {
		t.Fatal("summary and cite are kept with the move", meta)
	}
}

func TestMetaPostsATorqueCommentBestEffort(t *testing.T) {
	h, f := taskHTTP(t)
	code, out := h.do(t, "PUT", "/tasks/CW-3/meta", `{"done_summary":"Usage index.","pr_cites":[{"owner":"hollis-labs","repo":"tachyon","number":229}]}`)
	if code != 200 || out["torque_comment"] != "posted" || !strings.Contains(f.posts[0], "Done: Usage index. (PR 229)") {
		t.Fatal(code, out, f.posts)
	}
	if code, _ = h.do(t, "PUT", "/tasks/CW-3/meta", `{"pr_cites":["nonsense"]}`); code != 400 {
		t.Fatal(code)
	}
	if code, _ = h.do(t, "PUT", "/tasks/NOPE-1/meta", `{}`); code != 404 {
		t.Fatal(code)
	}
}

func TestCommentWithStatusAndPinIsOneRevision(t *testing.T) {
	h, _ := taskHTTP(t)
	th := mk(t, h.Owned, "themes", "T", Item{"status": "proposed"})
	path := "/themes/" + th["id"].(string) + "/comments"
	code, out := h.do(t, "POST", path, `{"text":"Ready @tachyon_pm","status":"ready","pin":true,"expected_revision":1}`)
	if code != 200 {
		t.Fatal(code, out)
	}
	item, applied, c := object(out["item"]), object(out["applied"]), object(out["comment"])
	if item["status"] != "ready" || item["pinned"] != true || number(item["rev"]) != 2 || applied["status"] != "ready" || applied["pin"] != true {
		t.Fatal("one write, one revision:", out)
	}
	if ms := array(c["mentions"]); len(ms) != 1 || object(ms[0])["token"] != "@tachyon_pm" {
		t.Fatal(c)
	}
	if code, _ = h.do(t, "POST", path, `{"text":"x","status":"bogus","expected_revision":2}`); code != 400 {
		t.Fatal("unknown theme status", code)
	}
	if code, _ = h.do(t, "POST", path, `{"text":"stale","expected_revision":1}`); code != 409 {
		t.Fatal(code)
	}
	if code, out = h.do(t, "POST", path, `{"text":"plain","expected_revision":2}`); code != 200 || len(object(out["applied"])) != 0 {
		t.Fatal(code, out)
	}
}

func TestTransitionCommentGoesToTheCommentsEndpoint(t *testing.T) {
	h, f := taskHTTP(t)
	if code, out := h.do(t, "POST", "/tasks/CW-3/transition", `{"to":"blocked","reason":"waiting on Tether"}`); code != 200 {
		t.Fatal(code, out)
	}
	if len(f.posts) != 2 || strings.Contains(f.posts[0], "waiting on Tether") || !strings.HasSuffix(strings.Fields(f.posts[0])[0], "/transition") || !strings.Contains(f.posts[1], "/comments") || !strings.Contains(f.posts[1], "waiting on Tether") {
		t.Fatal("Torque's REST transition ignores a comment, so it must be posted separately:", f.posts)
	}
}

func TestCreateThemeTaskUsesTheThemesProjectAndTag(t *testing.T) {
	h, f := taskHTTP(t)
	ws := mk(t, h.Owned, "workstreams", "WS", Item{"torque_tag": "planner", "torque_project_ids": []any{"CW-P1"}})
	th := mk(t, h.Owned, "themes", "Theme", Item{"workstream_ids": []any{ws["id"]}})
	path := "/themes/" + th["id"].(string) + "/tasks"
	code, out := h.do(t, "POST", path, `{"title":"  New thing "}`)
	task := object(out["task"])
	if code != 201 || task["id"] != "CW-9" || task["title"] != "New thing" || task["status"] != "todo" {
		t.Fatal(code, out)
	}
	if got := f.posts[len(f.posts)-1]; !strings.Contains(got, `"project_id":"CW-P1"`) || !strings.Contains(got, `"tags":["planner"]`) {
		t.Fatal(got)
	}
	if code, _ := h.do(t, "POST", path, `{"title":""}`); code != 400 {
		t.Fatal("empty title", code)
	}
	bare := mk(t, h.Owned, "themes", "Bare", Item{})
	if code, _ := h.do(t, "POST", "/themes/"+bare["id"].(string)+"/tasks", `{"title":"x"}`); code != 422 {
		t.Fatal("unlinked theme", code)
	}
}

func TestTimelineReleaseMilestoneFromTheThemesReleaseField(t *testing.T) {
	h, _ := taskHTTP(t)
	ws := mk(t, h.Owned, "workstreams", "WS", Item{"torque_tag": "planner"})
	find := func(th Item) Item {
		code, out := h.do(t, "GET", "/themes/"+th["id"].(string)+"/timeline", "")
		if code != 200 {
			t.Fatal(code, out)
		}
		for _, m := range array(out["milestones"]) {
			if object(m)["key"] == "release" {
				return object(m)
			}
		}
		return nil
	}
	plain := mk(t, h.Owned, "themes", "Plain", Item{"workstream_ids": []any{ws["id"]}})
	if find(plain) != nil {
		t.Fatal("no release field means no release milestone")
	}
	open := mk(t, h.Owned, "themes", "Open", Item{"workstream_ids": []any{ws["id"]}, "release": Item{"label": "v1 tag", "note": "after login-link", "task_id": "CW-3"}})
	if m := find(open); m == nil || m["label"] != "v1 tag" || m["note"] != "after login-link" || m["state"] == "reached" {
		t.Fatalf("task still todo: %v", m)
	}
	done := mk(t, h.Owned, "themes", "Done", Item{"workstream_ids": []any{ws["id"]}, "release": Item{"label": "v1 tag", "task_id": "CW-2"}})
	if m := find(done); m == nil || m["state"] != "reached" {
		t.Fatalf("done task reaches the milestone: %v", m)
	}
	dated := mk(t, h.Owned, "themes", "Dated", Item{"release": Item{"label": "v2", "released_at": "2026-10-30T10:00:00Z"}})
	if m := find(dated); m == nil || m["state"] != "reached" || m["at"] != "2026-10-30T10:00:00Z" {
		t.Fatalf("released_at: %v", m)
	}
}
