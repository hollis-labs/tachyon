package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestWorkVerbHTTPMapping(t *testing.T) {
	cases := []struct {
		verb, payload, method, path string
		body                        map[string]any
		query                       map[string]string
		response                    string
	}{
		{"work_create", `{"title":"Plan","metadata":{"owner":"keep"}}`, "POST", "/api/v1/tasks", map[string]any{"title": "Plan", "project_id": "default-project", "manual": true, "metadata": map[string]any{"owner": "keep"}}, nil, `{"id":"CW-1","title":"Plan","manual":true}`},
		{"work_read", `{"id":"CW-1"}`, "GET", "/api/v1/tasks/CW-1", nil, nil, `{"id":"CW-1"}`},
		{"work_update", `{"id":"CW-1","description":"","priority":0}`, "PUT", "/api/v1/tasks/CW-1", map[string]any{"description": "", "priority": float64(0)}, nil, `{"id":"CW-1","description":""}`},
		{"work_list", `{"status":"doing,review","tags":"a,b","limit":2,"offset":4}`, "GET", "/api/v1/tasks", nil, map[string]string{"project_id": "default-project", "status": "doing,review", "tags": "a,b", "limit": "2", "offset": "4"}, `{"tasks":[{"id":"CW-1"}],"total":10,"has_more":true,"next_offset":6}`},
		{"work_search", `{"query":"hello & world"}`, "GET", "/api/v1/tasks/search", nil, map[string]string{"q": "hello & world", "limit": "200", "offset": "0"}, `{"tasks":[{"id":"CW-1"}]}`},
		{"work_transition", `{"id":"CW-1","status":"review"}`, "POST", "/api/v1/tasks/CW-1/transition", map[string]any{"status": "review"}, nil, `{"id":"CW-1","status":"review"}`},
		{"work_comment", `{"id":"CW-1","author":"tester","content":"Ready"}`, "POST", "/api/v1/tasks/CW-1/comments", map[string]any{"author": "tester", "content": "Ready"}, nil, `{"id":1,"entity_id":"CW-1","author":"tester","content":"Ready"}`},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("wrong route: %s %s", r.Method, r.URL)
				}
				if len(r.URL.Query()) != len(tc.query) {
					t.Errorf("unexpected query: %v", r.URL.Query())
				}
				for k, v := range tc.query {
					if r.URL.Query().Get(k) != v {
						t.Errorf("query %s: %s", k, r.URL.Query().Get(k))
					}
				}
				if tc.body != nil {
					if r.Header.Get("Content-Type") != "application/json" {
						t.Error("missing JSON content type")
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(body, tc.body) {
						t.Errorf("body: %#v want %#v", body, tc.body)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tc.response))
			}))
			defer server.Close()
			p := &plugin{adapter: NewTorqueAdapter(server.URL), defaultProject: "default-project"}
			command, err := p.Command(context.Background(), subprocess.CommandRequest{Name: tc.verb, Args: tc.payload})
			if err != nil {
				t.Fatal(err)
			}
			var env contract.ResultEnvelope
			if err := json.Unmarshal([]byte(command.Content), &env); err != nil || env.Status != contract.StatusOK || !called {
				t.Fatalf("verb result: %s, %v", command.Content, err)
			}
			if tc.verb == "work_list" || tc.verb == "work_search" {
				var list WorkList
				if err := json.Unmarshal(env.Data, &list); err != nil {
					t.Fatal(err)
				}
				if len(list.Tasks) != 1 {
					t.Fatalf("lost list: %s", env.Data)
				}
				if tc.verb == "work_list" && (!list.HasMore || list.Total != 10 || list.NextOffset == nil || *list.NextOffset != 6) {
					t.Fatalf("lost pagination: %s", env.Data)
				}
				if tc.verb == "work_search" && list.Total != 1 {
					t.Fatalf("search total: %s", env.Data)
				}
			}
		})
	}
}

func TestAssignPreservesMetadata(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/tasks/CW-1" {
			t.Errorf("wrong assignment path: %s", r.URL.Path)
		}
		switch r.Method {
		case "GET":
			w.Write([]byte(`{"id":"CW-1","metadata":{"owner":"keep","nested":{"flag":true},"assignee":"old"}}`))
		case "PUT":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			want := map[string]any{"metadata": map[string]any{"owner": "keep", "nested": map[string]any{"flag": true}, "assignee": "new"}}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("clobbered metadata or unrelated fields: %#v", body)
			}
			w.Write([]byte(`{"id":"CW-1","metadata":{"assignee":"new","owner":"keep"}}`))
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()
	p := &plugin{adapter: NewTorqueAdapter(server.URL)}
	result, err := p.HandleVerb(context.Background(), "work_assign", json.RawMessage(`{"id":"CW-1","assignee":"new"}`))
	if err != nil || result.Status != contract.StatusOK || calls != 2 {
		t.Fatalf("assignment: %+v %v calls=%d", result, err, calls)
	}
}

func TestValidationDoesNotCallProvider(t *testing.T) {
	p := &plugin{} // any adapter call would panic
	for _, tc := range []struct{ verb, payload string }{
		{"work_create", `{}`}, {"work_create", `{`}, {"work_read", `null`}, {"work_read", `{}`},
		{"work_update", `{"id":"CW-1","status":"done"}`}, {"work_assign", `{"id":"CW-1"}`},
		{"work_transition", `{"id":"CW-1"}`}, {"work_comment", `{"id":"CW-1"}`},
		{"work_search", `{"query":" "}`}, {"work_list", `{"limit":201}`}, {"work_list", `[]`},
	} {
		env, err := p.HandleVerb(context.Background(), tc.verb, json.RawMessage(tc.payload))
		if err != nil || env.Status != contract.StatusError || env.Error.Code != "validation" {
			t.Errorf("%s %s: %+v %v", tc.verb, tc.payload, env, err)
		}
	}
	caps := p.Capabilities()
	if err := caps.Validate(); err != nil {
		t.Fatal(err)
	}
	command, err := p.Command(context.Background(), subprocess.CommandRequest{Name: "plugin_capabilities"})
	if err != nil || !strings.Contains(command.Content, `"work_assign"`) {
		t.Fatal("missing discovery declaration")
	}
	if _, err := p.Command(context.Background(), subprocess.CommandRequest{Name: "unknown"}); err == nil {
		t.Fatal("unknown command admitted")
	}
}

func TestProviderFailures(t *testing.T) {
	for _, response := range []struct {
		status int
		body   string
	}{{422, `{"error":"bad transition"}`}, {200, `invalid JSON`}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(response.status)
			w.Write([]byte(response.body))
		}))
		p := &plugin{adapter: NewTorqueAdapter(server.URL)}
		env, err := p.HandleVerb(context.Background(), "work_read", json.RawMessage(`{"id":"CW-1"}`))
		server.Close()
		if err != nil || env.Status != contract.StatusError || env.Error.Code != "provider_error" {
			t.Fatalf("provider failure: %+v %v", env, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewTorqueAdapter("http://127.0.0.1:1").GetWorkItem(ctx, "CW-1"); err == nil {
		t.Fatal("cancelled request succeeded")
	}
}
