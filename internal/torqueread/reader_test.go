package torqueread

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeTorque serves the three allowed resources and records every request.
type fakeTorque struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []string
	auth []string
}

func newFake(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *fakeTorque {
	t.Helper()
	f := &fakeTorque{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.RequestURI())
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

const secret = "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func fullTask(id string) map[string]any {
	return map[string]any{
		"id": id, "kind": "agent", "title": "Fix the thing", "status": "doing", "priority": 2,
		"description": "Plain text. token " + secret + " end.", "tags": []any{"chat", map[string]any{"Name": "ws:a"}},
		"created_at": "2026-10-24T15:00:00Z", "updated_at": "2026-10-24T16:00:00Z",
		"project_id": "PRJ-1", "epic_id": "EP-1", "sprint_id": "", "depends_on": []string{"CW-0"}, "blocked_reason": "",
		// Execution-side fields that must never reach an agent.
		"system_prompt": "SECRET PROMPT", "environment": "ENV=1", "permissions": "all", "tools": "bash",
		"files": "/etc/passwd", "working_dir": "/root", "executor": "cli", "metadata": "{\"k\":1}", "source_ref": "x",
		"launch_profile": "p", "agent_profile": "a", "cost_budget": 5, "token_budget": 9,
	}
}

func TestGetTaskKeepsOnlyTheSafeSubsetAndRedacts(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(fullTask("CW-1")) })
	r := New(f.URL, "tok")
	got, err := r.GetTask(context.Background(), "CW-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	out := string(raw)
	for _, banned := range []string{"SECRET PROMPT", "ENV=1", "/etc/passwd", "/root", "system_prompt", "environment", "working_dir", "executor", "launch_profile", "cost_budget", secret} {
		if strings.Contains(out, banned) {
			t.Fatalf("leaked %q in %s", banned, out)
		}
	}
	if !strings.Contains(out, "[REDACTED]") || got.ContentTrust != ContentTrust || got.Title != "Fix the thing" || got.ProjectID != "PRJ-1" {
		t.Fatalf("unexpected result: %s", out)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "chat" || got.Tags[1] != "ws:a" {
		t.Fatalf("tags: %v", got.Tags)
	}
	if f.reqs[0] != "GET /api/v1/tasks/CW-1" || f.auth[0] != "Bearer tok" {
		t.Fatalf("request: %v %v", f.reqs, f.auth)
	}
}

func TestGetTaskBoundsAndFlagsALongDescription(t *testing.T) {
	long := strings.Repeat("é", 10_000) // 20,000 bytes, multi-byte
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		m := fullTask("CW-2")
		m["description"] = long
		json.NewEncoder(w).Encode(m)
	})
	got, err := New(f.URL, "").GetTask(context.Background(), "CW-2")
	if err != nil {
		t.Fatal(err)
	}
	if !got.DescriptionTruncated || got.DescriptionBytes != 20_000 || len(got.Description) > GetDescriptionBytes {
		t.Fatalf("truncation: truncated=%v bytes=%d len=%d", got.DescriptionTruncated, got.DescriptionBytes, len(got.Description))
	}
	if !json.Valid([]byte(fmt.Sprintf("%q", got.Description))) || strings.ContainsRune(got.Description, '�') {
		t.Fatal("cut inside a rune")
	}
}

func TestIDsAndPathsAreValidatedAndOnlyGETIsUsed(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); w.Write([]byte(`{}`)) })
	r := New(f.URL, "")
	for _, bad := range []string{"", "../x", "a/b", "a b", "-x", strings.Repeat("a", 201), "x?y=1", "x#y"} {
		if _, err := r.GetTask(context.Background(), bad); err != ErrInvalid {
			t.Fatalf("id %q accepted: %v", bad, err)
		}
		if _, err := r.Comments(context.Background(), bad, 0, ""); err != ErrInvalid {
			t.Fatalf("comments id %q accepted: %v", bad, err)
		}
	}
	if len(f.reqs) != 0 {
		t.Fatalf("invalid ids reached Torque: %v", f.reqs)
	}
	if _, err := r.ListTasks(context.Background(), ListFilter{Filters: map[string]string{"system_prompt": "x"}}); err != ErrInvalid {
		t.Fatalf("unknown filter accepted: %v", err)
	}
	if _, err := r.ListTasks(context.Background(), ListFilter{Filters: map[string]string{"status": "todo;drop"}}); err != ErrInvalid {
		t.Fatalf("odd filter value accepted: %v", err)
	}
	if _, err := r.ListTasks(context.Background(), ListFilter{Limit: MaxList + 1}); err != ErrInvalid {
		t.Fatalf("limit over max accepted: %v", err)
	}
	r.GetTask(context.Background(), "CW-1")
	r.ListTasks(context.Background(), ListFilter{})
	r.Comments(context.Background(), "CW-1", 0, "")
	for _, q := range f.reqs {
		if !strings.HasPrefix(q, "GET /api/v1/tasks") {
			t.Fatalf("unexpected request %q", q)
		}
	}
}

func TestStatusesMapToTypedErrorsWithoutLeakingTheBody(t *testing.T) {
	for code, want := range map[int]error{404: ErrNotFound, 500: ErrUnavailable, 401: ErrUnavailable, 403: ErrUnavailable} {
		f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			w.Write([]byte("internal secret detail"))
		})
		_, err := New(f.URL, "").GetTask(context.Background(), "CW-1")
		if err == nil || !strings.HasPrefix(err.Error(), want.Error()) || strings.Contains(err.Error(), "internal secret detail") {
			t.Fatalf("status %d: %v", code, err)
		}
	}
	// A redirect is never followed, so a credential cannot be bounced elsewhere.
	hit := false
	other := newFake(t, func(w http.ResponseWriter, r *http.Request) { hit = true })
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) })
	if _, err := New(f.URL, "tok").GetTask(context.Background(), "CW-1"); err == nil || hit {
		t.Fatalf("redirect followed: err=%v hit=%v", err, hit)
	}
}

// Torque's comments endpoint rejects unknown parameters (offset gets a 400), so
// the fake does too. It honours limit and returns its own cursor.
func TestCommentsUseTorquesCursorAndShrinkToTheSizeBudget(t *testing.T) {
	big := strings.Repeat("x", CommentBytes+500)
	var limits []string
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		for k := range r.URL.Query() {
			switch k {
			case "limit", "cursor", "sort_by", "sort_dir":
			default:
				http.Error(w, `{"error":"unknown query parameter: `+k+`"}`, http.StatusBadRequest)
				return
			}
		}
		limits = append(limits, r.URL.Query().Get("limit"))
		n := 20
		if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l < n {
			n = l
		}
		items := []map[string]any{}
		for i := 0; i < n; i++ {
			items = append(items, map[string]any{"id": 100 + i, "author": "a", "content": big + secret, "created_at": "2026-10-24T00:00:00Z", "entity_id": "CW-1", "entity_type": "task"})
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items, "meta": map[string]any{"has_more": true, "next_cursor": "cur_" + strconv.Itoa(n)}})
	})
	page, err := New(f.URL, "").Comments(context.Background(), "CW-1", 20, "start_CUR")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	if len(raw) > ResponseBytes+1024 || page.Returned == 0 || page.Returned >= 20 || !page.HasMore {
		t.Fatalf("budget: returned=%d has_more=%v bytes=%d", page.Returned, page.HasMore, len(raw))
	}
	// The cursor must follow exactly the comments returned: it comes from the request that fit.
	if page.NextCursor != "cur_"+strconv.Itoa(page.Returned) || len(limits) < 2 || limits[len(limits)-1] != strconv.Itoa(page.Returned) {
		t.Fatalf("cursor %q returned %d limits %v", page.NextCursor, page.Returned, limits)
	}
	for _, c := range page.Items {
		if !c.ContentTruncated || len(c.Content) > CommentBytes || strings.Contains(c.Content, secret) {
			t.Fatalf("comment not bounded/redacted: %d truncated=%v", len(c.Content), c.ContentTruncated)
		}
	}
	if !strings.Contains(f.reqs[0], "cursor=start_CUR") || strings.Contains(strings.Join(f.reqs, " "), "offset") {
		t.Fatalf("requests %v", f.reqs)
	}
	if _, err := New(f.URL, "").Comments(context.Background(), "CW-1", 0, "has space"); err != ErrInvalid {
		t.Fatalf("bad cursor: %v", err)
	}
	if _, err := New(f.URL, "").Comments(context.Background(), "CW-1", 51, ""); err != ErrInvalid {
		t.Fatalf("limit over max: %v", err)
	}
}

func TestCommentsSmallPageIsOneRequestAndLastPageHasNoCursor(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": 1, "author": "a", "content": "hi", "created_at": "t"}}, "meta": map[string]any{"has_more": false}})
	})
	page, err := New(f.URL, "").Comments(context.Background(), "CW-1", 0, "")
	if err != nil || page.Returned != 1 || page.HasMore || page.NextCursor != "" || len(f.reqs) != 1 || !strings.Contains(f.reqs[0], "limit=10") {
		t.Fatalf("page=%+v err=%v reqs=%v", page, err, f.reqs)
	}
}

func TestListBriefRecordsWithCursorPassThrough(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request) {
		a := fullTask("CW-1")
		a["description"] = strings.Repeat("d", 1000)
		json.NewEncoder(w).Encode(map[string]any{"items": []any{a, fullTask("CW-2")}, "meta": map[string]any{"has_more": true, "next_cursor": "abc_DEF-123"}})
	})
	page, err := New(f.URL, "").ListTasks(context.Background(), ListFilter{Filters: map[string]string{"status": "doing", "project_id": "PRJ-1"}, Limit: 2, Cursor: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Returned != 2 || !page.HasMore || page.NextCursor != "abc_DEF-123" || len(page.Items[0].Description) > ListDescription || !page.Items[0].DescriptionTruncated {
		t.Fatalf("page: %+v", page)
	}
	if page.Items[0].CreatedAt != "" || len(page.Items[0].DependsOn) != 0 {
		t.Fatal("list should be brief")
	}
	q := f.reqs[0]
	for _, want := range []string{"status=doing", "project_id=PRJ-1", "limit=2", "cursor=c1", "sort_by=updated_at"} {
		if !strings.Contains(q, want) {
			t.Fatalf("query %q missing %q", q, want)
		}
	}
}

func TestAccessKind(t *testing.T) {
	if AccessKind("plan") != "plan" || AccessKind("agent") != "task" || AccessKind("issue") != "task" || AccessKind("task") != "task" {
		t.Fatal("kind mapping")
	}
}
