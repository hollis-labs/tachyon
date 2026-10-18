package main

import (
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
		{Method: "POST", Path: objectsPrefix + "/sessions/s1/launch", Body: []byte(`{}`)},
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
