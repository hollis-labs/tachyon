package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
)

func TestObjectsForwardProjectIdentityAndStampWriter(t *testing.T) {
	urn := "msg://agent/agent-mux/prj_abc1234567"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/registry/projects/"+url.PathEscape(urn) || r.Method != "PATCH" || r.URL.RawQuery != "" {
			t.Errorf("unexpected target: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer private-token" || r.Header.Get("Tether-Protocol") != "1" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect upstream authority")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["display_name"] != "Renamed" || body["last_updated_by"] != "tachyon:operator" {
			t.Errorf("unexpected update: %v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"project","display_name":"Renamed"}`))
	}))
	defer upstream.Close()
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("private-token"), 0600); err != nil {
		t.Fatal(err)
	}
	h := NewObjectHandler(upstream.URL, file)
	res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: "PATCH", Path: objectsPrefix + "/projects/item", RawQuery: "urn=" + url.QueryEscape(urn) + "&full=true&include=callback", Headers: map[string]string{"Authorization": "Bearer browser-token", "Cookie": "browser-cookie"}, Body: []byte(`{"display_name":"Renamed","tags":[]}`)})
	if res.Status != 200 || res.Headers["Cache-Control"] != "no-store" {
		t.Fatalf("response: %+v", res)
	}
}

func TestObjectsRejectUnreviewedOperationsBeforeUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected upstream request")
		w.WriteHeader(500)
	}))
	defer upstream.Close()
	h := NewObjectHandler(upstream.URL, "")
	for _, req := range []subprocess.HTTPRequest{
		{Method: "POST", Path: objectsPrefix + "/catalog/plans", Body: []byte(`{}`)},
		{Method: "DELETE", Path: objectsPrefix + "/workstreams/ws-1"},
		{Method: "POST", Path: objectsPrefix + "/sessions/s1/stop", Body: []byte(`{}`)},
		{Method: "GET", Path: objectsPrefix + "/projects/item", Query: map[string]string{"urn": "msg://agent/agent-mux/agt_123"}},
		{Method: "PATCH", Path: objectsPrefix + "/workstreams/../reload", Body: []byte(`{}`)},
		{Method: "POST", Path: objectsPrefix + "/projects", Body: []byte(`{"display_name":"x","callback":{"target":"file:///secret"}}`)},
		{Method: "POST", Path: objectsPrefix + "/projects", Body: []byte(`null`)},
		{Method: "PATCH", Path: objectsPrefix + "/workstreams/ws-1", Body: []byte(`{"name":null}`)},
		{Method: "GET", Path: objectsPrefix + "/workstreams", RawQuery: "status=deleted"},
	} {
		if got := h.Handle(context.Background(), req); got.Status < 400 {
			t.Errorf("accepted %s %s", req.Method, req.Path)
		}
	}
}

func TestObjectsWorkstreamEnsureClearAndFiltering(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			if r.URL.Path != "/workstreams" || r.URL.Query().Get("status") != "closed" || r.URL.Query().Get("workflow_id") != "torque:workflow" || r.URL.Query().Get("ref") != "" {
				t.Errorf("query: %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"workstreams":[]}`))
		case "POST":
			if r.URL.Path != "/sessions/s1/workstream" {
				t.Error(r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["ensure"] == true {
				_, _ = w.Write([]byte(`{"id":"ws-1","status":"active"}`))
				return
			}
			if id, ok := body["workstream_id"]; !ok || id != "" {
				t.Errorf("clear changed: %v", body)
			}
			w.WriteHeader(204)
		}
	}))
	defer upstream.Close()
	h := NewObjectHandler(upstream.URL, "")
	for _, body := range []string{`{"ensure":true}`, `{"workstream_id":""}`} {
		res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: "POST", Path: objectsPrefix + "/sessions/s1/workstream", Body: []byte(body)})
		if res.Status != 200 && res.Status != 204 {
			t.Fatal(res.Status)
		}
	}
	res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: "GET", Path: objectsPrefix + "/workstreams", RawQuery: "status=closed&workflow_id=torque%3Aworkflow&ref=url%3Aprivate"})
	if res.Status != 200 {
		t.Fatal(res.Status)
	}
}

func TestObjectsQuickLaunchBoundary(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("Tether-Protocol") != "1" {
			t.Error("incorrect launch authority")
		}
		switch r.URL.Path {
		case "/sessions":
			if r.Method == "GET" {
				if r.URL.Query().Get("state") != "running" || r.URL.Query().Get("limit") != "1000" || r.URL.Query().Get("cursor") != "next" {
					t.Error(r.URL)
				}
			} else {
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["launch"] != "review" || body["prompt_append"] != "Review carefully" || body["idempotency_key"] != "key-1" {
					t.Errorf("body: %v, %v", body, err)
				}
			}
		case "/sessions/s1/launch", "/sessions/s1", "/workers":
			if r.URL.RawQuery != "" {
				t.Error("forwarded unsupported query")
			}
		default:
			t.Errorf("unexpected route %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"id":"s1"}`))
	}))
	defer upstream.Close()
	h := NewObjectHandler(upstream.URL, "")
	for _, req := range []subprocess.HTTPRequest{
		{Method: "POST", Path: objectsPrefix + "/sessions", Body: []byte(`{"launch":"review","worker":"worker-1","prompt_append":"Review carefully","idempotency_key":"key-1"}`)},
		{Method: "POST", Path: objectsPrefix + "/sessions/s1/launch", Body: []byte(`{}`)},
		{Method: "GET", Path: objectsPrefix + "/sessions/s1", RawQuery: "full=true"},
		{Method: "GET", Path: objectsPrefix + "/workers"},
		{Method: "GET", Path: objectsPrefix + "/sessions", RawQuery: "state=exited&cursor=next"},
	} {
		if res := h.Handle(context.Background(), req); res.Status != 200 {
			t.Errorf("response: %+v", res)
		}
	}
	before := calls
	for _, body := range []string{`{"launch":"review","override":"{}"}`, `{"launch":"review","agent_inline":"{}"}`, `{"launch":"review","worker":null}`} {
		if res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: "POST", Path: objectsPrefix + "/sessions", Body: []byte(body)}); res.Status != 400 {
			t.Error(res.Status)
		}
	}
	if res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: "POST", Path: objectsPrefix + "/sessions/s1/launch", Body: []byte(`{"force":true}`)}); res.Status != 400 {
		t.Error(res.Status)
	}
	if calls != before {
		t.Fatal("unsupported launch payload reached upstream")
	}
}

func TestActivityReadsAreBoundedAndCannotDrainMailOrExposeOutput(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.Header.Get("Cookie") != "" {
			t.Error("invalid read authority")
		}
		switch r.URL.Path {
		case "/events":
			q := r.URL.Query()
			if q.Get("limit") != "200" || q.Get("since_seq") != "12" || q.Get("cursor") != "20" || q.Get("session_id") != "s1" {
				t.Errorf("unbounded query: %s", r.URL)
			}
			found := false
			for _, kind := range q["kind"] {
				if kind == "session.output" {
					t.Error("raw output allowed")
				}
				if kind == "tool.call.started" {
					found = true
				}
			}
			if !found {
				t.Error("missing tool activity")
			}
		case "/registry/bindings":
			if r.URL.Query().Get("current") != "true" || r.URL.Query().Get("target_urn") != "msg://agent/agent-mux/a1" {
				t.Error(r.URL)
			}
		case "/sessions/s1/health", "/messages/m1/trace":
			if r.URL.RawQuery != "" {
				t.Error("unreviewed query forwarded")
			}
		default:
			t.Errorf("unexpected route %s", r.URL)
		}
		if r.URL.Path == "/events" {
			_, _ = w.Write([]byte(`{"events":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()
	h := NewObjectHandler(upstream.URL, "")
	for _, req := range []subprocess.HTTPRequest{
		{Method: "GET", Path: objectsPrefix + "/events", RawQuery: "since_seq=12&cursor=20&session_id=s1&limit=99999&kind=session.output"},
		{Method: "GET", Path: objectsPrefix + "/binding", RawQuery: "urn=msg%3A%2F%2Fagent%2Fagent-mux%2Fa1"},
		{Method: "GET", Path: objectsPrefix + "/sessions/s1/health", RawQuery: "full=true"},
		{Method: "GET", Path: objectsPrefix + "/messages/m1/trace", RawQuery: "as=someone"},
	} {
		if res := h.Handle(context.Background(), req); res.Status != 200 {
			t.Error(res.Status)
		}
	}
	before := calls
	for _, req := range []subprocess.HTTPRequest{
		{Method: "GET", Path: objectsPrefix + "/events", RawQuery: "cursor=-1"},
		{Method: "GET", Path: objectsPrefix + "/events", RawQuery: "session_id=../other"},
		{Method: "GET", Path: objectsPrefix + "/events/stream"},
		{Method: "GET", Path: objectsPrefix + "/messages/inbox"},
		{Method: "POST", Path: objectsPrefix + "/messages/m1/trace"},
		{Method: "GET", Path: objectsPrefix + "/binding", RawQuery: "urn=http://other"},
	} {
		if res := h.Handle(context.Background(), req); res.Status < 400 {
			t.Error("unreviewed read allowed")
		}
	}
	if calls != before {
		t.Fatal("unreviewed operation reached upstream")
	}
}

func TestObjectsPlanAndAgentReadsAreProjected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer private-token" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		switch r.URL.Path {
		case "/catalog/plans/cp_928c2c714c131ae1c23ff24b":
			_, _ = w.Write([]byte(`{"plan_id":"cp_928c2c714c131ae1c23ff24b","name":"Add agent","state":"applied","diff":"SECRET DIFF","validation_ok":true,
				"planned_by":{"principal":"x"},"files":[{"path":"agents/a.yaml","action":"create","new_sha256":"abc"}]}`))
		case "/registry/agents":
			if r.URL.Query().Get("status") != "*" {
				t.Errorf("status: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"agents":[{"urn":"msg://agent/x/agt_other00000","display_name":"Other"},
				{"urn":"msg://agent/agent-mux/agt_abcde12345","display_name":"Reviewer","role":"review","status":"active","callback":{"target":"file:///secret"},"host_address":"10.0.0.1","tags":["a"]}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("private-token"), 0600); err != nil {
		t.Fatal(err)
	}
	h := NewObjectHandler(upstream.URL, file)
	get := func(p string) subprocess.HTTPResponse {
		return h.Handle(context.Background(), subprocess.HTTPRequest{Method: "GET", Path: objectsPrefix + p})
	}
	plan := get("/plans/cp_928c2c714c131ae1c23ff24b")
	if plan.Status != 200 || bytes.Contains(plan.Body, []byte("SECRET")) || bytes.Contains(plan.Body, []byte("sha256")) || !bytes.Contains(plan.Body, []byte("agents/a.yaml")) {
		t.Fatalf("plan: %d %s", plan.Status, plan.Body)
	}
	agent := get("/agents/agt_abcde12345")
	if agent.Status != 200 || !bytes.Contains(agent.Body, []byte("Reviewer")) || bytes.Contains(agent.Body, []byte("secret")) || bytes.Contains(agent.Body, []byte("10.0.0.1")) {
		t.Fatalf("agent: %d %s", agent.Status, agent.Body)
	}
	if got := get("/agents/agt_zzzzz99999"); got.Status != 404 {
		t.Fatalf("missing agent: %d", got.Status)
	}
	for _, p := range []string{"/plans/../reload", "/plans/cp_short", "/agents/agt_x", "/agents/msg:%2F%2Fagent"} {
		if got := get(p); got.Status != 400 && got.Status != 404 {
			t.Errorf("accepted %s: %d", p, got.Status)
		}
	}
}

func TestPerSessionEventQueriesCanOnlyNarrowTheReviewedWindow(t *testing.T) {
	var got []url.Values
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Query())
		_, _ = w.Write([]byte(`{"events":[]}`))
	}))
	defer upstream.Close()
	h := NewObjectHandler(upstream.URL, "")
	for _, raw := range []string{
		"session_id=s1&limit=20",
		"session_id=s1&limit=1&kind=session.work_state_changed",
		"session_id=s1&limit=5000&kind=session.output", // wider/raw asks fall back to the reviewed set
		"session_id=s1&limit=0&kind=session.work_state_changed&kind=session.output",
	} {
		if res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: "GET", Path: objectsPrefix + "/events", RawQuery: raw}); res.Status != 200 {
			t.Fatalf("%s: %d", raw, res.Status)
		}
	}
	want := []struct {
		limit string
		kinds int
	}{{"20", len(activityEventKinds)}, {"1", 1}, {"200", len(activityEventKinds)}, {"200", len(activityEventKinds)}}
	for i, w := range want {
		if got[i].Get("limit") != w.limit || len(got[i]["kind"]) != w.kinds || got[i].Get("session_id") != "s1" {
			t.Errorf("query %d: %v", i, got[i])
		}
		for _, k := range got[i]["kind"] {
			if k == "session.output" {
				t.Errorf("query %d requested raw output", i)
			}
		}
	}
	if got[1]["kind"][0] != "session.work_state_changed" {
		t.Errorf("work state kind not requested: %v", got[1])
	}
}
