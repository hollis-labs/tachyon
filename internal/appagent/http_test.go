package appagent

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/approvals"
)

func newHandler(t *testing.T, rt *fakeRuntime) (*Handler, *Service, *httptest.Server) {
	t.Helper()
	s, _ := newService(t, rt)
	h := &Handler{Service: s}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return h, s, srv
}

func call(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestHandlerRoutesAndErrors(t *testing.T) {
	_, _, srv := newHandler(t, &fakeRuntime{events: []TurnEvent{{Kind: EventText, Text: "hi"}, {Kind: EventFinish, Reason: "stop"}}})
	code, out := call(t, srv, "GET", Prefix+"/threads", "")
	threads, _ := out["threads"].([]any)
	if code != 200 || len(threads) != 1 {
		t.Fatalf("threads: %d %v", code, out)
	}
	th := threads[0].(map[string]any)
	cap := th["capability"].(map[string]any)
	ag := th["agent"].(map[string]any)
	if th["id"] != DefaultThread || cap["mode"] != "live" || cap["canSend"] != true || ag["kind"] != "agent" || th["owner"] != "tachyon" {
		t.Fatalf("thread shape: %v", th)
	}
	if code, _ := call(t, srv, "POST", Prefix+"/threads/nope/messages", `{"body":"x"}`); code != 404 {
		t.Fatalf("unknown thread: %d", code)
	}
	for _, body := range []string{``, `{}`, `{"body":""}`, `{"body":"x","extra":1}`, `not json`} {
		if code, _ := call(t, srv, "POST", Prefix+"/threads/"+DefaultThread+"/messages", body); code != 400 {
			t.Fatalf("body %q: %d", body, code)
		}
	}
	code, out = call(t, srv, "POST", Prefix+"/threads/"+DefaultThread+"/messages", `{"body":"hello"}`)
	if code != 202 || out["message"].(map[string]any)["from"] != "operator" || out["reply"].(map[string]any)["from"] != "agent" {
		t.Fatalf("send: %d %v", code, out)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		code, out = call(t, srv, "GET", Prefix+"/threads/"+DefaultThread+"/messages?limit=10", "")
		msgs, _ := out["messages"].([]any)
		if code == 200 && len(msgs) == 2 && msgs[1].(map[string]any)["body"] == "hi" && msgs[1].(map[string]any)["streaming"] != true {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("history never completed: %d %v", code, out)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _ := call(t, srv, "GET", Prefix+"/threads/"+DefaultThread+"/messages?limit=999", ""); code != 400 {
		t.Fatalf("limit over max: %d", code)
	}
	if code, _ := call(t, srv, "GET", Prefix+"/threads/"+DefaultThread+"/messages?before_seq=-1", ""); code != 400 {
		t.Fatalf("negative cursor: %d", code)
	}
	if code, _ := call(t, srv, "POST", Prefix+"/threads/"+DefaultThread+"/read", ""); code != 200 {
		t.Fatalf("read: %d", code)
	}
	if code, _ := call(t, srv, "POST", Prefix+"/threads/nope/read", ""); code != 404 {
		t.Fatalf("read unknown: %d", code)
	}
	if code, _ := call(t, srv, "DELETE", Prefix+"/threads", ""); code != 405 && code != 404 {
		t.Fatalf("delete: %d", code)
	}
}

func TestHandlerRefusesASendWhileAReplyIsRunning(t *testing.T) {
	rt := &fakeRuntime{hold: make(chan struct{}), events: []TurnEvent{{Kind: EventFinish, Reason: "stop"}}}
	_, _, srv := newHandler(t, rt)
	call(t, srv, "GET", Prefix+"/threads", "")
	if code, _ := call(t, srv, "POST", Prefix+"/threads/"+DefaultThread+"/messages", `{"body":"one"}`); code != 202 {
		t.Fatalf("first: %d", code)
	}
	code, out := call(t, srv, "POST", Prefix+"/threads/"+DefaultThread+"/messages", `{"body":"two"}`)
	e, _ := out["error"].(map[string]any)
	if code != 409 || e["code"] != "busy" {
		t.Fatalf("second: %d %v", code, out)
	}
	close(rt.hold)
}

func TestHandlerWithoutAServiceSaysWhy(t *testing.T) {
	for _, h := range []*Handler{nil, {}, {Disabled: "Set TACHYON_APP_AGENT_CONFIG."}} {
		srv := httptest.NewServer(h)
		code, out := call(t, srv, "GET", Prefix+"/threads", "")
		e, _ := out["error"].(map[string]any)
		if code != 503 || e["code"] != "not_configured" || e["message"] == "" {
			t.Fatalf("%+v: %d %v", h, code, out)
		}
		srv.Close()
	}
}

func TestEventsStreamStartsWithTheInFlightSnapshotThenDeltas(t *testing.T) {
	rt := &fakeRuntime{hold: make(chan struct{})}
	_, s, srv := newHandler(t, rt)
	s.Threads(context.Background())
	_, reply, err := s.Send(context.Background(), DefaultThread, "hi")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	s.mu.Lock()
	l := s.live[reply.ID]
	s.mu.Unlock()
	s.apply(l, TurnEvent{Kind: EventText, Text: "so far"})

	resp, err := http.Get(srv.URL + Prefix + "/threads/" + DefaultThread + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") || resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers: %d %v", resp.StatusCode, resp.Header)
	}
	rd := bufio.NewReader(resp.Body)
	next := func() Event {
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				var e Event
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
					t.Fatal(err)
				}
				return e
			}
		}
	}
	if e := next(); e.Type != "message" || e.Message.Body != "so far" || !e.Message.Streaming {
		t.Fatalf("snapshot: %+v", e)
	}
	s.apply(l, TurnEvent{Kind: EventText, Text: " and more"})
	if e := next(); e.Type != "delta" || e.Text != " and more" {
		t.Fatalf("delta: %+v", e)
	}
	close(rt.hold)
	s.apply(l, TurnEvent{Kind: EventFinish, Reason: "stop"})
	if e := next(); e.Type != "end" || e.MessageID != reply.ID {
		t.Fatalf("end: %+v", e)
	}
	if code, _ := call(t, srv, "GET", Prefix+"/threads/nope/events", ""); code != 404 {
		t.Fatalf("events for unknown thread: %d", code)
	}
}

// The operator session cookie is scoped to approvals.CookiePath. A route outside
// it never receives the cookie, so every send is refused as "session required"
// even though the operator is connected (found live on 2026-10-24).
func TestPrefixIsInsideTheOperatorSessionCookiePath(t *testing.T) {
	if !strings.HasPrefix(Prefix+"/", approvals.CookiePath) {
		t.Fatalf("Prefix %q is outside the operator cookie path %q", Prefix, approvals.CookiePath)
	}
}
