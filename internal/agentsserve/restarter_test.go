package agentsserve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Tether side of a restart, against Tether's routes: a 409 resume is ErrResumeRefused; a relaunch creates the
// session with continuity "relaunch" and then launches it; the dropped scopes come from session.scopes_narrowed.
func TestTetherRestarterRoutes(t *testing.T) {
	var calls []string
	var created map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "GET" && r.URL.Path == "/sessions/old":
			fmt.Fprint(w, `{"id":"old","launch_id":"chief","worker_id":"worker_1","state":"running"}`)
		case r.URL.Path == "/logical-agents/la1/resume":
			w.WriteHeader(409)
			fmt.Fprint(w, `{"error":{"code":"conflict","message":"resume source old is placed on worker w-secret-1; resume on a worker is not enabled"}}`)
		case r.Method == "POST" && r.URL.Path == "/sessions":
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(201)
			fmt.Fprint(w, `{"id":"new"}`)
		case r.URL.Path == "/sessions/new/launch":
			fmt.Fprint(w, `{"id":"new","state":"running"}`)
		case r.URL.Path == "/events":
			if r.URL.Query().Get("kind") != "session.scopes_narrowed" {
				t.Error("events read without the kind filter")
			}
			fmt.Fprint(w, `{"events":[{"session_id":"other","payload_json":"{\"dropped\":[\"x\"]}"},{"session_id":"new","payload_json":"{\"requested\":[\"tether.admin\",\"tether.read\"],\"granted\":[\"tether.read\"],\"dropped\":[\"tether.admin\"],\"basis\":\"launcher\"}"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	r := &TetherRestarter{Base: srv.URL, Client: srv.Client()}
	ctx := context.Background()
	place, err := r.Placement(ctx, "old")
	if err != nil || place.Launch != "chief" || place.Worker != "worker_1" {
		t.Fatal(place, err)
	}
	launch := place.Launch
	_, err = r.Resume(ctx, "la1", "k")
	if !errors.Is(err, ErrResumeRefused) || !strings.Contains(err.Error(), "409 (conflict)") || strings.Contains(err.Error(), "w-secret-1") {
		t.Fatalf("a refused resume: %v", err)
	}
	id, err := r.Relaunch(ctx, launch, "k")
	if err != nil || id != "new" || created["launch"] != "chief" || created["continuity"] != "relaunch" || created["idempotency_key"] != "k" {
		t.Fatalf("relaunch: %q %v %v", id, err, created)
	}
	dropped, err := r.ScopesDropped(ctx, "new")
	if err != nil || fmt.Sprint(dropped) != "[tether.admin]" {
		t.Fatal(dropped, err)
	}
	if none, err := r.ScopesDropped(ctx, "absent"); err != nil || none != nil {
		t.Fatal(none, err)
	}
	if strings.Join(calls[:5], ",") != "GET /sessions/old,POST /logical-agents/la1/resume,POST /sessions,POST /sessions/new/launch,GET /events" {
		t.Fatalf("calls: %v", calls)
	}
	if _, err := r.Placement(ctx, "missing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a missing session: %v", err)
	}
}
