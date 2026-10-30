package chattopics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/chatapi"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const base = "/api/plugins/console/v1/chat"

type env struct {
	h      *Handler
	srv    *httptest.Server
	events []string
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{}
	e.h = &Handler{
		Store: &Store{DB: db},
		Exists: func(_ context.Context, ref string) error {
			if ref == "app:ghost" {
				return errors.New("none")
			}
			return nil
		},
		MessageSeq: func(_ context.Context, t chatapi.ThreadRef, m string) (int64, error) {
			seqs := map[string]int64{"m1": 1, "m2": 2, "m3": 3}
			if s, ok := seqs[m]; ok && t.ID != "other" {
				return s, nil
			}
			return 0, errors.New("not in thread")
		},
		Published: func(_ context.Context, thread, typ, op, id string) { e.events = append(e.events, typ+" "+op) },
	}
	e.srv = httptest.NewServer(e.h.Mux())
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

func TestTopicsCRUDAndJump(t *testing.T) {
	e := setup(t)
	code, a := e.do(t, "POST", "/threads/app:t1/topics", `{"title":"  Later one ","message_ref":"m3"}`)
	if code != 201 || a["title"] != "Later one" || a["seq"] != 3.0 || a["added_by"] != "user" || a["message_ref"] != "m3" {
		t.Fatal(code, a)
	}
	e.do(t, "POST", "/threads/app:t1/topics", `{"title":"Early","message_ref":"m1"}`)
	_, list := e.do(t, "GET", "/threads/app:t1/topics", "")
	ts := list["topics"].([]any)
	if len(ts) != 2 || ts[0].(map[string]any)["title"] != "Early" { // ordered by position in the conversation
		t.Fatalf("order: %v", ts)
	}
	for body, want := range map[string]int{
		`{"title":"","message_ref":"m1"}`:                                 400,
		`{"title":"x","message_ref":""}`:                                  400,
		`{"title":"x","message_ref":"nope"}`:                              400, // not a message of this thread
		`{"title":"x","message_ref":"m1","extra":true}`:                   400,
		`{"title":"` + strings.Repeat("a", 201) + `","message_ref":"m1"}`: 400,
	} {
		if code, out := e.do(t, "POST", "/threads/app:t1/topics", body); code != want {
			t.Errorf("%.40s: %d %v", body, code, out)
		}
	}
	if code, _ := e.do(t, "POST", "/threads/app:other/topics", `{"title":"x","message_ref":"m1"}`); code != 400 {
		t.Errorf("message of another thread: %d", code)
	}
	id := a["id"].(string)
	if code, _ := e.do(t, "DELETE", "/threads/app:t2/topics/"+id, ""); code != 404 {
		t.Errorf("cross-thread delete: %d", code)
	}
	if code, _ := e.do(t, "DELETE", "/threads/app:t1/topics/"+id, ""); code != 200 {
		t.Errorf("delete: %d", code)
	}
	if code, _ := e.do(t, "DELETE", "/threads/app:t1/topics/"+id, ""); code != 404 {
		t.Errorf("second delete: %d", code)
	}
	if code, _ := e.do(t, "GET", "/threads/app:ghost/topics", ""); code != 404 {
		t.Errorf("ghost: %d", code)
	}
	if code, _ := e.do(t, "GET", "/threads/zzz/topics", ""); code != 400 {
		t.Errorf("bad ref: %d", code)
	}
	if got := strings.Join(e.events, ","); got != "topic add,topic add,topic remove" {
		t.Fatalf("events: %s", got)
	}
}

func TestBookmarksAreIdempotentPerMessage(t *testing.T) {
	e := setup(t)
	code, a := e.do(t, "POST", "/threads/app:t1/bookmarks", `{"message_ref":"m2","note":"the plan"}`)
	if code != 201 || a["note"] != "the plan" {
		t.Fatal(code, a)
	}
	code, b := e.do(t, "POST", "/threads/app:t1/bookmarks", `{"message_ref":"m2"}`)
	if code != 200 || b["id"] != a["id"] || b["note"] != "the plan" {
		t.Fatalf("repeat without note: %d %v", code, b)
	}
	code, c := e.do(t, "POST", "/threads/app:t1/bookmarks", `{"message_ref":"m2","note":"renamed"}`)
	if code != 200 || c["id"] != a["id"] || c["note"] != "renamed" {
		t.Fatalf("repeat with note: %d %v", code, c)
	}
	// Same message in another thread is a different bookmark.
	if code, d := e.do(t, "POST", "/threads/app:t9/bookmarks", `{"message_ref":"m2"}`); code != 201 || d["id"] == a["id"] {
		t.Fatalf("other thread: %d %v", code, d)
	}
	_, list := e.do(t, "GET", "/threads/app:t1/bookmarks", "")
	if len(list["bookmarks"].([]any)) != 1 {
		t.Fatalf("list: %v", list)
	}
	if code, _ := e.do(t, "POST", "/threads/app:t1/bookmarks", `{"message_ref":"nope"}`); code != 400 {
		t.Errorf("unknown message: %d", code)
	}
	if code, _ := e.do(t, "POST", "/threads/app:t1/bookmarks", `{"message_ref":"m1","note":"`+strings.Repeat("n", 501)+`"}`); code != 400 {
		t.Errorf("long note: %d", code)
	}
	if code, _ := e.do(t, "DELETE", "/threads/app:t1/bookmarks/"+a["id"].(string), ""); code != 200 {
		t.Errorf("delete: %d", code)
	}
	if code, _ := e.do(t, "DELETE", "/threads/app:t1/bookmarks/"+a["id"].(string), ""); code != 404 {
		t.Errorf("second delete: %d", code)
	}
	if got := strings.Join(e.events, ","); got != "bookmark add,bookmark add,bookmark remove" { // the repeats emit nothing
		t.Fatalf("events: %s", got)
	}
}

func TestLimitsAndPurge(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for i := 0; i < MaxTopics; i++ {
		if _, err := e.h.Store.AddTopic(ctx, "app:t1", "t", "m1", nil, "agent"); err != nil {
			t.Fatal(i, err)
		}
	}
	if code, out := e.do(t, "POST", "/threads/app:t1/topics", `{"title":"one more","message_ref":"m1"}`); code != 413 || out["code"] != "limit_reached" {
		t.Fatalf("topic cap: %d %v", code, out)
	}
	e.h.Store.AddBookmark(ctx, "app:t1", "m1", "")
	if err := e.h.Store.PurgeThread(ctx, "app:t1"); err != nil {
		t.Fatal(err)
	}
	if ts, _ := e.h.Store.Topics(ctx, "app:t1"); len(ts) != 0 {
		t.Fatal("topics survived purge")
	}
	if bs, _ := e.h.Store.Bookmarks(ctx, "app:t1"); len(bs) != 0 {
		t.Fatal("bookmarks survived purge")
	}
}

func TestWritesRefusedWithoutOperatorSession(t *testing.T) {
	e := setup(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer up.Close()
	guarded := approvals.New(up.URL).RequireOperator(e.h.Mux())
	call := func(method, path, origin string, cookie bool) int {
		r := httptest.NewRequest(method, "https://tachyon.test"+base+path, strings.NewReader(`{"title":"x","message_ref":"m1"}`))
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
	for _, w := range [][2]string{{"POST", "/threads/app:t1/topics"}, {"DELETE", "/threads/app:t1/topics/x"}, {"POST", "/threads/app:t1/bookmarks"}, {"DELETE", "/threads/app:t1/bookmarks/x"}} {
		if c := call(w[0], w[1], "https://tachyon.test", false); c != 401 && c != 403 {
			t.Errorf("%v without session: %d", w, c)
		}
		if c := call(w[0], w[1], "https://evil.test", true); c != 403 {
			t.Errorf("%v cross-origin: %d", w, c)
		}
	}
	if c := call("GET", "/threads/app:t1/topics", "", false); c != 200 {
		t.Errorf("open read: %d", c)
	}
}
