package chatthreads

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/appagent"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/chatbridge"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

type fakeRuntime struct {
	mu      sync.Mutex
	submits int
	result  appagent.Result
}

func (f *fakeRuntime) Ensure(context.Context, string) (string, error) { return "sess", nil }
func (f *fakeRuntime) Submit(context.Context, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submits++
	return "turn", nil
}
func (f *fakeRuntime) Observe(context.Context, string, string, func(appagent.TurnEvent)) error {
	return io.ErrUnexpectedEOF // lost stream: the service falls back to Result
}
func (f *fakeRuntime) Result(context.Context, string, string) (appagent.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.result, nil
}

type env struct {
	h     *Handler
	db    *localstore.Store
	rt    *fakeRuntime
	srv   *httptest.Server
	owned *chatbridge.Owned
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rt := &fakeRuntime{result: appagent.Result{State: "completed", Text: "hello from the agent"}}
	svc := &appagent.Service{Store: &appagent.Store{DB: db}, Runtime: rt, Label: "Tachyon", PollEvery: 5 * time.Millisecond, PollFor: 2 * time.Second}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Stop)
	owned := &chatbridge.Owned{Store: db, SourceID: "src"}
	h := &Handler{Agg: &Aggregator{App: svc, Chat: owned, State: &StateStore{DB: db}}, Hub: &Hub{DB: db}}
	svc.OnChange = func(id string) { h.Notify(context.Background(), "app:"+id) }
	srv := httptest.NewServer(h.Mux())
	t.Cleanup(srv.Close)
	return &env{h: h, db: db, rt: rt, srv: srv, owned: owned}
}

func (e *env) do(t *testing.T, method, path, body string, hdr ...string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+"/api/plugins/console/v1/chat"+path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (e *env) seedTurn(t *testing.T, id, session, kind, content, created string, resolved bool) {
	t.Helper()
	turn := chatbridge.Turn{ID: id, AgentID: "agt_demo", SessionID: session, TurnID: "tn-" + id, Kind: kind, Content: content, Created: created,
		Revision: 1, State: "presented", Replyable: kind == "question"}
	if resolved {
		turn.Resolution = &chatbridge.Resolution{ID: "r", Text: "my answer", At: created}
		turn.State = "resolved"
	}
	raw, _ := json.Marshal(turn)
	err := e.db.Writer.Submit(context.Background(), "seed", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_turns(id,source,channel,publication_id,launch_id,session_id,created_at,revision,body) VALUES(?,?,?,?,?,?,?,?,?)`,
			id, "src", "ch", "", "", session, created, 1, raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func threads(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	var res []map[string]any
	for _, v := range out["threads"].([]any) {
		res = append(res, v.(map[string]any))
	}
	return res
}

func TestUnifiedListBlockedPinArchiveSearchLens(t *testing.T) {
	e := setup(t)
	e.seedTurn(t, "a1", "s1", "question", "Which branch should I use?", "2099-10-30T10:00:00Z", false)
	e.seedTurn(t, "a2", "s2", "terminal", "Done. Two nits.", "2099-10-30T09:00:00Z", false)
	e.seedTurn(t, "a3", "", "terminal", "no session, no thread", "2099-10-30T09:30:00Z", false)

	_, out := e.do(t, "GET", "/threads", "")
	ts := threads(t, out)
	if len(ts) != 3 { // default app thread + two Tether sessions
		t.Fatalf("threads: %v", ts)
	}
	by := map[string]map[string]any{}
	for _, th := range ts {
		by[th["thread_ref"].(string)] = th
	}
	if by["tether:s1"]["blocked_on_user"] != 1.0 || by["tether:s2"]["blocked_on_user"] != 0.0 || by["tether:s1"]["source"] != "tether" {
		t.Fatalf("blocked: %v", by)
	}
	if by["tether:s1"]["unread"] != 1.0 {
		t.Fatalf("unread: %v", by["tether:s1"])
	}
	if ts[0]["thread_ref"] != "tether:s1" { // newest first, nothing pinned
		t.Fatalf("order: %v", ts[0]["thread_ref"])
	}

	_, out = e.do(t, "GET", "/threads?need_you=true", "")
	if ts = threads(t, out); len(ts) != 1 || ts[0]["thread_ref"] != "tether:s1" {
		t.Fatalf("need_you: %v", ts)
	}

	// Pin the older one: it sorts first.
	if code, _ := e.do(t, "PATCH", "/threads/tether:s2", `{"pinned":true,"title":"Review PR"}`); code != 200 {
		t.Fatal(code)
	}
	_, out = e.do(t, "GET", "/threads", "")
	if ts = threads(t, out); ts[0]["thread_ref"] != "tether:s2" || ts[0]["title"] != "Review PR" || ts[0]["pinned"] != true {
		t.Fatalf("pin: %v", ts[0])
	}

	// Archive moves it to the archived lens only.
	e.do(t, "PATCH", "/threads/tether:s2", `{"archived":true}`)
	_, out = e.do(t, "GET", "/threads", "")
	if ts = threads(t, out); len(ts) != 2 {
		t.Fatalf("archived still listed: %v", ts)
	}
	_, out = e.do(t, "GET", "/threads?lens=archived", "")
	if ts = threads(t, out); len(ts) != 1 || ts[0]["thread_ref"] != "tether:s2" {
		t.Fatalf("archived lens: %v", ts)
	}

	// Search: Tether turn text and App message bodies.
	_, out = e.do(t, "GET", "/threads?q=WHICH+BRANCH", "")
	if ts = threads(t, out); len(ts) != 1 || ts[0]["thread_ref"] != "tether:s1" {
		t.Fatalf("search tether: %v", ts)
	}
	code, out := e.do(t, "POST", "/threads/app:app-tachyon/messages", `{"text":"zebra crossing","idempotency_key":"k0"}`)
	if code != 202 {
		t.Fatal(code, out)
	}
	_, out = e.do(t, "GET", "/threads?q=zebra", "")
	if ts = threads(t, out); len(ts) != 1 || ts[0]["thread_ref"] != "app:app-tachyon" {
		t.Fatalf("search app body: %v", ts)
	}

	// Work lens needs a project (plans arrive with T2).
	_, out = e.do(t, "GET", "/threads?lens=work", "")
	if len(threads(t, out)) != 0 {
		t.Fatal("work lens should be empty")
	}
	e.do(t, "PATCH", "/threads/tether:s1", `{"project_id":"PRJ-1"}`)
	_, out = e.do(t, "GET", "/threads?lens=work", "")
	if ts = threads(t, out); len(ts) != 1 || ts[0]["project_id"] != "PRJ-1" {
		t.Fatalf("work lens: %v", ts)
	}
	e.do(t, "PATCH", "/threads/tether:s1", `{"project_id":null}`)
	_, out = e.do(t, "GET", "/threads/tether:s1", "")
	if out["project_id"] != nil {
		t.Fatalf("project not cleared: %v", out)
	}

	// Validation.
	for path, want := range map[string]int{"/threads?lens=nope": 400, "/threads?limit=0": 400, "/threads/bogus": 400, "/threads/tether:missing": 404, "/threads/app:missing/messages": 404} {
		if code, _ := e.do(t, "GET", path, ""); code != want {
			t.Errorf("GET %s: %d, want %d", path, code, want)
		}
	}
	if code, _ := e.do(t, "PATCH", "/threads/app:missing", `{"pinned":true}`); code != 404 {
		t.Errorf("patch missing: %d", code)
	}
	if code, _ := e.do(t, "PATCH", "/threads/tether:s1", `{"bogus":1}`); code != 400 {
		t.Errorf("patch unknown field: %d", code)
	}
}

func TestTetherMessagesReadAndNoOpenQuestion(t *testing.T) {
	e := setup(t)
	e.seedTurn(t, "b1", "s9", "question", "Pick one?", "2020-10-30T10:00:00Z", true)
	e.seedTurn(t, "b2", "s9", "terminal", "All done.", "2020-10-30T10:05:00Z", false)
	_, out := e.do(t, "GET", "/threads/tether:s9/messages", "")
	ms := out["messages"].([]any)
	if len(ms) != 3 || ms[1].(map[string]any)["role"] != "operator" || ms[1].(map[string]any)["text"] != "my answer" || ms[2].(map[string]any)["seq"] != 3.0 {
		t.Fatalf("messages: %v", ms)
	}
	// Nothing is waiting for a reply.
	if code, out := e.do(t, "POST", "/threads/tether:s9/messages", `{"text":"hi","idempotency_key":"k"}`); code != 409 || out["code"] != "conflict" {
		t.Fatal(code, out)
	}
	// Read clears unread on a Tether thread.
	_, th := e.do(t, "GET", "/threads/tether:s9", "")
	if th["unread"] != 2.0 {
		t.Fatalf("unread before: %v", th["unread"])
	}
	if code, _ := e.do(t, "POST", "/threads/tether:s9/read", ""); code != 200 {
		t.Fatal(code)
	}
	if _, th = e.do(t, "GET", "/threads/tether:s9", ""); th["unread"] != 0.0 {
		t.Fatalf("unread after: %v", th["unread"])
	}
}

func TestCreateThreadAndIdempotentSend(t *testing.T) {
	e := setup(t)
	code, th := e.do(t, "POST", "/threads", `{"agent":"tachyon","title":"Plan the week","project_id":"PRJ-9"}`)
	if code != 201 || th["title"] != "Plan the week" || th["source"] != "app" || th["project_id"] != "PRJ-9" {
		t.Fatal(code, th)
	}
	ref := th["thread_ref"].(string)
	if code, _ := e.do(t, "POST", "/threads", `{"agent":"bad agent!"}`); code != 400 {
		t.Fatalf("bad agent: %d", code)
	}
	send := func(key string) map[string]any {
		code, out := e.do(t, "POST", "/threads/"+ref+"/messages", `{"text":"hello","idempotency_key":"`+key+`"}`)
		if code != 202 || out["state"] != "accepted" {
			t.Fatal(code, out)
		}
		return out
	}
	first := send("k1")
	// Wait for the first reply so a second distinct send is not refused as busy.
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, m := e.do(t, "GET", "/threads/"+ref+"/messages", "")
		ms := m["messages"].([]any)
		if len(ms) == 2 && ms[1].(map[string]any)["meta"].(map[string]any)["streaming"] == nil {
			if ms[1].(map[string]any)["text"] != "hello from the agent" {
				t.Fatalf("reply: %v", ms[1])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reply never completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	again := send("k1")
	if again["id"] != first["id"] || again["seq"] != first["seq"] {
		t.Fatalf("replayed key made a new message: %v vs %v", first, again)
	}
	e.rt.mu.Lock()
	n := e.rt.submits
	e.rt.mu.Unlock()
	if n != 1 {
		t.Fatalf("runtime submitted %d times", n)
	}
	if code, _ := e.do(t, "POST", "/threads/"+ref+"/messages", `{"text":"x"}`); code != 400 {
		t.Fatalf("missing key: %d", code)
	}
}

func TestBlockedAppThreadCountsAsBlocker(t *testing.T) {
	e := setup(t)
	e.rt.result = appagent.Result{State: "input_required", Text: "Which project?"}
	e.do(t, "GET", "/threads", "")
	e.do(t, "POST", "/threads/app:app-tachyon/messages", `{"text":"plan it","idempotency_key":"k2"}`)
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, th := e.do(t, "GET", "/threads/app:app-tachyon", "")
		if th["blocked_on_user"] == 1.0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never blocked: %v", th)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, out := e.do(t, "GET", "/threads?need_you=true", "")
	if len(threads(t, out)) != 1 {
		t.Fatal("need_you should list the blocked app thread")
	}
}

// sse reads frames from an open stream.
type sse struct {
	sc  *bufio.Scanner
	res *http.Response
}

func (e *env) stream(t *testing.T, path string, hdr ...string) *sse {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/plugins/console/v1/chat"+path, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatal(err, res)
	}
	t.Cleanup(func() { res.Body.Close() })
	return &sse{sc: bufio.NewScanner(res.Body), res: res}
}

// next returns the next frame's id (0 if none) and decoded data.
func (s *sse) next(t *testing.T) (string, map[string]any) {
	t.Helper()
	type r struct {
		id string
		m  map[string]any
	}
	ch := make(chan r, 1)
	go func() {
		var id string
		for s.sc.Scan() {
			l := s.sc.Text()
			if v, ok := strings.CutPrefix(l, "id: "); ok {
				id = v
			}
			if v, ok := strings.CutPrefix(l, "data: "); ok {
				var m map[string]any
				_ = json.Unmarshal([]byte(v), &m)
				ch <- r{id, m}
				return
			}
		}
		ch <- r{}
	}()
	select {
	case v := <-ch:
		return v.id, v.m
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a frame")
		return "", nil
	}
}

func TestEventStreamIdsAndResume(t *testing.T) {
	e := setup(t)
	e.do(t, "GET", "/threads", "") // creates the default app thread
	ctx := context.Background()
	ref := "app:app-tachyon"
	for i, typ := range []string{"plan", "card", "panel", "activity"} {
		if _, err := e.h.Hub.Publish(ctx, ref, typ, map[string]any{"n": i}); err != nil {
			t.Fatal(err)
		}
	}
	// Full replay from id 0 is not done (no Last-Event-ID = live only); resume after the first id.
	rows, _ := e.h.Hub.Replay(ctx, ref, 0)
	if len(rows) != 3 { // panel is live-only and was never logged
		t.Fatalf("logged: %d", len(rows))
	}
	s := e.stream(t, "/threads/"+ref+"/events", "Last-Event-ID", itoa(rows[0].ID))
	id, ev := s.next(t)
	if id != itoa(rows[1].ID) || ev["type"] != "card" || ev["thread_ref"] != ref || ev["at"] == nil {
		t.Fatalf("first replayed: %s %v", id, ev)
	}
	if id, ev = s.next(t); ev["type"] != "activity" || id != itoa(rows[2].ID) {
		t.Fatalf("second replayed: %s %v", id, ev)
	}
	// Live: a signal arrives without an id, a durable event with one.
	_, _ = e.h.Hub.Publish(ctx, ref, "panel", map[string]any{"panel": "plan"})
	if id, ev = s.next(t); id != "" || ev["type"] != "panel" {
		t.Fatalf("live panel: %s %v", id, ev)
	}
	seq, _ := e.h.Hub.Publish(ctx, ref, "topic", map[string]any{"op": "add"})
	if id, ev = s.next(t); id != itoa(seq) || ev["type"] != "topic" {
		t.Fatalf("live topic: %s %v", id, ev)
	}
	// Another thread's events are not delivered here.
	_, _ = e.h.Hub.Publish(ctx, "tether:other", "topic", map[string]any{})
	_, _ = e.h.Hub.Publish(ctx, ref, "panel", map[string]any{"panel": "end"})
	if _, ev = s.next(t); ev["panel"] != "end" {
		t.Fatalf("leaked: %v", ev)
	}

	if code, _ := e.do(t, "GET", "/threads/"+ref+"/events", "", "Last-Event-ID", "x"); code != 400 {
		t.Fatalf("bad id: %d", code)
	}
	if code, _ := e.do(t, "GET", "/threads/app:missing/events", ""); code != 404 {
		t.Fatalf("missing thread: %d", code)
	}
}

func TestAppLiveFramesAndSpine(t *testing.T) {
	e := setup(t)
	e.do(t, "GET", "/threads", "")
	thread := e.stream(t, "/threads/app:app-tachyon/events")
	spine := e.stream(t, "/events")
	e.do(t, "POST", "/threads/app:app-tachyon/messages", `{"text":"hi","idempotency_key":"k"}`)
	seen := map[string]bool{}
	for i := 0; i < 12 && !(seen["message"] && seen["end"]); i++ {
		_, ev := thread.next(t)
		if ev["thread_ref"] != "app:app-tachyon" {
			t.Fatalf("envelope: %v", ev)
		}
		seen[ev["type"].(string)] = true
	}
	if !seen["message"] || !seen["end"] {
		t.Fatalf("app frames: %v", seen)
	}
	// The spine carries `thread` summaries only.
	_, ev := spine.next(t)
	if ev["type"] != "thread" || ev["thread_ref"] != "app:app-tachyon" {
		t.Fatalf("spine: %v", ev)
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	e := setup(t)
	sub := e.h.Hub.Subscribe("app:x")
	for i := 0; i < 300; i++ {
		_, _ = e.h.Hub.Publish(context.Background(), "app:x", "panel", nil)
	}
	n := 0
	for range sub.C {
		n++
	}
	if n != 256 {
		t.Fatalf("drained %d frames before close", n)
	}
	e.h.Hub.Unsubscribe(sub) // safe after a drop
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// The real routes are mounted behind RequireOperator in server.go; this proves the
// contract: reads pass, writes need an operator session and a same-origin Origin.
func TestWritesRefusedWithoutOperatorSessionOrOrigin(t *testing.T) {
	e := setup(t)
	var elevated bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if elevated {
			w.Write([]byte(`{"elevated":true,"assurance":"operator-token"}`))
			return
		}
		w.WriteHeader(403)
		w.Write([]byte(`{"elevated":false}`))
	}))
	defer up.Close()
	guarded := approvals.New(up.URL).RequireOperator(e.h.Mux())
	call := func(method, path, origin string, cookie bool, body ...string) int {
		r := httptest.NewRequest(method, "https://tachyon.test/api/plugins/console/v1/chat"+path, strings.NewReader(strings.Join(body, "")))
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
	writes := [][2]string{{"POST", "/threads"}, {"PATCH", "/threads/app:app-tachyon"}, {"POST", "/threads/app:app-tachyon/messages"}, {"POST", "/threads/app:app-tachyon/read"}}
	for _, wr := range writes {
		if c := call(wr[0], wr[1], "https://tachyon.test", false); c != 401 && c != 403 {
			t.Errorf("%v without session: %d", wr, c)
		}
		if c := call(wr[0], wr[1], "https://evil.test", true); c != 403 {
			t.Errorf("%v cross-origin: %d", wr, c)
		}
		if c := call(wr[0], wr[1], "", true); c != 403 {
			t.Errorf("%v no origin: %d", wr, c)
		}
	}
	if c := call("GET", "/threads", "", false); c != 200 {
		t.Errorf("open read: %d", c)
	}
	elevated = true
	if c := call("POST", "/threads", "https://tachyon.test", true, `{"agent":"tachyon"}`); c != 201 {
		t.Errorf("elevated create: %d", c)
	}
}

func TestPlanMessagesAndProgressAppearInThreads(t *testing.T) {
	e := setup(t)
	e.do(t, "GET", "/threads", "")
	e.h.Agg.PlanProgress = func(_ context.Context, refs []string) map[string]Progress {
		return map[string]Progress{"app:app-tachyon": {Done: 2, Total: 5}}
	}
	if _, err := e.h.Agg.App.AppendNote(context.Background(), "app-tachyon", "plan", "Ship it", map[string]any{"plan_id": "pl_1"}); err != nil {
		t.Fatal(err)
	}
	_, out := e.do(t, "GET", "/threads/app:app-tachyon/messages", "")
	ms := out["messages"].([]any)
	m := ms[len(ms)-1].(map[string]any)
	if m["kind"] != "plan" || m["text"] != "Ship it" || m["role"] != "agent" || m["meta"].(map[string]any)["plan_id"] != "pl_1" {
		t.Fatalf("plan message: %v", m)
	}
	_, th := e.do(t, "GET", "/threads/app:app-tachyon", "")
	pp := th["plan_progress"].(map[string]any)
	if pp["done"] != 2.0 || pp["total"] != 5.0 || th["unread"] != 1.0 {
		t.Fatalf("thread: %v", th)
	}
	_, out = e.do(t, "GET", "/threads?lens=work", "")
	if len(threads(t, out)) != 1 {
		t.Fatal("a thread with a plan belongs to the work lens")
	}
	if _, err := e.h.Agg.App.AppendNote(context.Background(), "app-tachyon", "text", "x", nil); err == nil {
		t.Fatal("AppendNote accepted kind text")
	}
	if _, err := e.h.Agg.App.AppendNote(context.Background(), "missing", "plan", "x", nil); err == nil {
		t.Fatal("AppendNote accepted a missing thread")
	}
}
