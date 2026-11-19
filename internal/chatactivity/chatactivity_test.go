package chatactivity

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
	"github.com/hollis-labs/tachyon/internal/localstore"
)

const base = "/api/plugins/console/v1/chat"

type env struct {
	rec    *Recorder
	h      *Handler
	mu     sync.Mutex
	events []map[string]any
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{}
	e.rec = &Recorder{Store: &Store{DB: db}, Publish: func(_ context.Context, thread, typ string, f map[string]any) {
		e.mu.Lock()
		defer e.mu.Unlock()
		m := map[string]any{"type": typ, "thread_ref": thread}
		for k, v := range f {
			m[k] = v
		}
		e.events = append(e.events, m)
	}}
	e.h = &Handler{Rec: e.rec, Exists: func(_ context.Context, ref string) error {
		if ref == "app:ghost" {
			return errors.New("none")
		}
		return nil
	}}
	return e
}

func (e *env) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	e.h.Mux().ServeHTTP(w, httptest.NewRequest(method, base+path, strings.NewReader(body)))
	var m map[string]any
	_ = json.NewDecoder(w.Body).Decode(&m)
	return w.Code, m
}

func (e *env) count(typ string) (n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range e.events {
		if ev["type"] == typ {
			n++
		}
	}
	return
}

func TestFeedIsAppendOnlyOrderedFilteredAndPaged(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		typ, actor := "plan.changed", "user"
		if i%2 == 1 {
			typ, actor = "task.closed", "agent:a1"
		}
		if _, err := e.rec.Record(ctx, Entry{ThreadRef: "app:t1", Type: typ, Actor: actor, Source: "tachyon", Payload: Card(fmt.Sprintf("item %d", i), "s", nil)}); err != nil {
			t.Fatal(err)
		}
	}
	e.rec.Quiet(ctx, "app:other", "plan.changed", "user", "tachyon", nil, nil)
	code, m := e.do(t, "GET", "/threads/app:t1/activity?limit=2", "")
	items := m["activity"].([]any)
	if code != 200 || len(items) != 2 || m["has_more"] != true || items[0].(map[string]any)["payload"].(map[string]any)["title"] != "item 4" {
		t.Fatalf("%d %v", code, m)
	}
	before := items[1].(map[string]any)["seq"]
	_, m = e.do(t, "GET", fmt.Sprintf("/threads/app:t1/activity?before_seq=%v&limit=50", before), "")
	if len(m["activity"].([]any)) != 3 || m["has_more"] != false {
		t.Fatalf("page 2: %v", m)
	}
	_, m = e.do(t, "GET", "/threads/app:t1/activity?type=task.closed", "")
	if len(m["activity"].([]any)) != 2 {
		t.Fatalf("type filter: %v", m)
	}
	_, m = e.do(t, "GET", "/threads/app:t1/activity?actor=agent:a1&q=ITEM%203", "")
	if len(m["activity"].([]any)) != 1 {
		t.Fatalf("actor+q filter: %v", m)
	}
	_, m = e.do(t, "GET", "/threads/app:t1/activity?q=%25", "")
	if len(m["activity"].([]any)) != 0 {
		t.Fatalf("a LIKE wildcard must match literally: %v", m)
	}
	for _, p := range []string{"?type=bogus", "?limit=0", "?limit=101", "?before_seq=-1", "?before_seq=x"} {
		if c, _ := e.do(t, "GET", "/threads/app:t1/activity"+p, ""); c != 400 {
			t.Errorf("%s: %d", p, c)
		}
	}
	if c, _ := e.do(t, "GET", "/threads/app:ghost/activity", ""); c != 404 {
		t.Errorf("ghost: %d", c)
	}
	if c, _ := e.do(t, "GET", "/threads/bogus/activity", ""); c != 400 {
		t.Errorf("bad ref: %d", c)
	}
	if e.count("activity") != 6 {
		t.Errorf("activity events: %d", e.count("activity"))
	}
}

func TestEntryValidationAndRedaction(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	ok := Entry{ThreadRef: "app:t1", Type: "note", Actor: "agent:a", Source: "agent:a"}
	for name, mut := range map[string]func(*Entry){
		"unknown type": func(x *Entry) { x.Type = "card.exploded" },
		"no actor":     func(x *Entry) { x.Actor = "" },
		"no source":    func(x *Entry) { x.Source = "" },
		"bad cite":     func(x *Entry) { x.Cite = []Cite{{Kind: "url", Ref: "http://x"}} },
		"empty ref":    func(x *Entry) { x.Cite = []Cite{{Kind: "task", Ref: " "}} },
		"too many":     func(x *Entry) { x.Cite = make([]Cite, 9) },
		"big payload":  func(x *Entry) { x.Payload = json.RawMessage(`"` + strings.Repeat("a", MaxPayload) + `"`) },
		"bad json":     func(x *Entry) { x.Payload = json.RawMessage(`{nope`) },
	} {
		x := ok
		mut(&x)
		if _, err := e.rec.Record(ctx, x); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if e.count("activity") != 0 {
		t.Error("a refused entry was published")
	}
	secret := "ghp_" + strings.Repeat("a1B2", 8)
	got, err := e.rec.Record(ctx, Entry{ThreadRef: "app:t1", Type: "note", Actor: "agent:a", Source: "agent:a", Payload: Card("token "+secret, strings.Repeat("x", 500), nil)})
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	_ = json.Unmarshal(got.Payload, &p)
	if strings.Contains(string(got.Payload), secret) || len([]rune(p["summary"].(string))) != 200 {
		t.Errorf("payload not bounded or redacted: %s", got.Payload)
	}
}

func TestCommentIsAlwaysTheUser(t *testing.T) {
	e := setup(t)
	code, m := e.do(t, "POST", "/threads/app:t1/activity/comments", `{"body":"looks right"}`)
	if code != 201 || m["actor"] != "user" || m["type"] != "comment" || m["payload"].(map[string]any)["body"] != "looks right" {
		t.Fatalf("%d %v", code, m)
	}
	for name, body := range map[string]string{
		"actor spoof": `{"body":"x","actor":"agent:evil"}`, "type spoof": `{"body":"x","type":"approval.decided"}`,
		"empty": `{"body":"  "}`, "long": `{"body":"` + strings.Repeat("a", MaxComment+1) + `"}`, "not json": `x`,
	} {
		if c, _ := e.do(t, "POST", "/threads/app:t1/activity/comments", body); c != 400 {
			t.Errorf("%s: %d", name, c)
		}
	}
	if c, _ := e.do(t, "POST", "/threads/app:ghost/activity/comments", `{"body":"x"}`); c != 404 {
		t.Errorf("ghost: %d", c)
	}
}

func TestWritesRequireAnOperatorSession(t *testing.T) {
	e := setup(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"elevated":false}`))
	}))
	defer up.Close()
	guarded := approvals.New(up.URL).RequireOperator(e.h.Mux())
	r := httptest.NewRequest("POST", "https://tachyon.test"+base+"/threads/app:t1/activity/comments", strings.NewReader(`{"body":"x"}`))
	r.Header.Set("Origin", "https://tachyon.test")
	w := httptest.NewRecorder()
	guarded.ServeHTTP(w, r)
	if w.Code != 401 && w.Code != 403 {
		t.Fatalf("no session: %d", w.Code)
	}
	if n := e.count("activity"); n != 0 {
		t.Fatal("a refused comment was recorded")
	}
	elevated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"elevated":true,"assurance":"operator-token"}`))
	}))
	defer elevated.Close()
	r = httptest.NewRequest("POST", "https://tachyon.test"+base+"/threads/app:t1/activity/comments", strings.NewReader(`{"body":"x"}`))
	r.Header.Set("Origin", "https://tachyon.test")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "s"})
	w = httptest.NewRecorder()
	approvals.New(elevated.URL).RequireOperator(e.h.Mux()).ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("elevated: %d %s", w.Code, w.Body)
	}
}

func TestPanelOpenAllowlistAndRateLimit(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if err := e.rec.OpenPanel(ctx, "app:t1", "terminal", "", "x", "agent:a", "agent:a"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a panel outside the allowlist: %v", err)
	}
	for i := 0; i < PanelsPerMinute; i++ {
		if err := e.rec.OpenPanel(ctx, "app:t1", "plan", "pl_1", "plan ready", "agent:a", "agent:a"); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := e.rec.OpenPanel(ctx, "app:t1", "plan", "", "", "agent:a", "agent:a"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("7th: %v", err)
	}
	if err := e.rec.OpenPanel(ctx, "app:t2", "work", "", "", "agent:a", "agent:a"); err != nil {
		t.Fatalf("another thread has its own budget: %v", err)
	}
	e.mu.Lock()
	var first map[string]any
	for _, ev := range e.events {
		if ev["type"] == "panel" {
			first = ev
			break
		}
	}
	e.mu.Unlock()
	if first["panel"] != "plan" || first["source"] != "agent" || first["ref"] != "pl_1" || first["reason"] != "plan ready" {
		t.Fatalf("panel event: %v", first)
	}
	_, m := e.do(t, "GET", "/threads/app:t1/activity?type=panel.opened", "")
	if len(m["activity"].([]any)) != PanelsPerMinute {
		t.Fatalf("panel.opened rows: %v", m)
	}
}

func TestContextMeter(t *testing.T) {
	e := setup(t)
	_, m := e.do(t, "GET", "/threads/app:t1/context", "")
	if m["source"] != "unknown" || m["used"] != nil || m["window"] != nil || m["model"] != nil || m["breakdown"] == nil {
		t.Fatalf("never reported: %v", m)
	}
	e.rec.SetContext(context.Background(), "app:t1", 48210, 0, "claude-sonnet-5-5")
	_, m = e.do(t, "GET", "/threads/app:t1/context", "")
	if m["source"] != "runtime" || m["used"] != 48210.0 || m["window"] != nil || m["model"] != "claude-sonnet-5-5" {
		t.Fatalf("used only: %v", m)
	}
	e.rec.SetContext(context.Background(), "app:t1", 60000, 200000, "")
	_, m = e.do(t, "GET", "/threads/app:t1/context", "")
	if m["used"] != 60000.0 || m["window"] != 200000.0 || m["model"] != "claude-sonnet-5-5" {
		t.Fatalf("a later report without a model keeps the known one: %v", m)
	}
	if e.count("context") != 2 {
		t.Errorf("context events: %d", e.count("context"))
	}
	_, m = e.do(t, "GET", "/threads/tether:s1/context", "")
	if m["source"] != "unknown" || m["used"] != nil {
		t.Fatalf("tether thread: %v", m)
	}
}

func TestFeedIsBoundedPerThreadAndPurgeable(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for i := 0; i < KeepPerThread+10; i++ {
		if _, err := e.rec.Store.Append(ctx, Entry{ThreadRef: "app:t1", Type: "tool.call", Actor: "agent", Source: "runtime"}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = e.rec.Store.DB.Reader.QueryRow(`SELECT COUNT(*) FROM chat_activity WHERE thread_ref='app:t1'`).Scan(&n)
	if n != KeepPerThread {
		t.Fatalf("kept %d", n)
	}
	e.rec.SetContext(ctx, "app:t1", 1, 2, "m")
	if err := e.rec.Store.PurgeThread(ctx, "app:t1"); err != nil {
		t.Fatal(err)
	}
	_ = e.rec.Store.DB.Reader.QueryRow(`SELECT (SELECT COUNT(*) FROM chat_activity)+(SELECT COUNT(*) FROM chat_context)`).Scan(&n)
	if n != 0 {
		t.Fatalf("purge left %d rows", n)
	}
}
