package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTetherSessionOperations(t *testing.T) {
	var created CreateSessionRequest
	var submitted string
	stopped := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sessions":
			var req struct {
				Launch         string `json:"launch"`
				BootPrompt     string `json:"boot_prompt"`
				IdempotencyKey string `json:"idempotency_key"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			created = CreateSessionRequest{LaunchID: req.Launch, BootPrompt: req.BootPrompt, IdempotencyKey: req.IdempotencyKey}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"s1","provider_id":"codex"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/s1":
			_, _ = w.Write([]byte(`{"id":"s1","launch_id":"launch-1","logical_agent_id":"a1","provider_id":"codex","state":"running"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/sessions":
			if q := r.URL.Query(); q.Get("state") != "running" || q.Get("limit") != "2" || q.Get("cursor") != "next" {
				t.Errorf("query: %v", q)
			}
			_, _ = w.Write([]byte(`{"sessions":[{"id":"s1","logical_agent_id":"a1","provider_id":"codex","state":"running"},{"id":"s2","logical_agent_id":"a2","provider_id":"claude","state":"running"}],"next_cursor":"more"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/sessions/s1/stop":
			stopped = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/sessions/s1/turn":
			var req struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			submitted = req.Text
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/s1/events":
			if q := r.URL.Query(); q.Get("limit") != "5" || q.Get("cursor") != "3" || q.Get("since_seq") != "2" {
				t.Errorf("history query: %v", q)
			}
			_, _ = w.Write([]byte(`{"events":[{"seq":4,"at":"2026-10-01T00:00:00Z","session_id":"s1","scope":"session","kind":"turn","payload_json":"{\"text\":\"hello\"}"}],"next_cursor":4}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	a, err := NewTetherAdapter(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req := CreateSessionRequest{LaunchID: "launch-1", BootPrompt: "hello", IdempotencyKey: "tachyon:create:1"}
	s, err := a.Create(ctx, req)
	if err != nil || s.ID != "s1" || s.AgentID != "a1" || created != req {
		t.Fatalf("create: %+v %+v %v", s, created, err)
	}
	page, err := a.List(ctx, ListSessionsRequest{AgentID: "a1", Provider: "codex", Status: "running", Limit: 2, Cursor: "next"})
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].ID != "s1" || page.NextCursor != "more" {
		t.Fatalf("list: %+v %v", page, err)
	}
	empty, err := a.List(ctx, ListSessionsRequest{AgentID: "missing", Status: "running", Limit: 2, Cursor: "next"})
	if err != nil || empty.Sessions == nil || len(empty.Sessions) != 0 || empty.NextCursor != "more" {
		t.Fatalf("empty filtered page: %+v %v", empty, err)
	}
	info, err := a.Attach(ctx, "s1")
	if err != nil || info.SessionID != "s1" || info.Transport != "tether" || !info.Streaming {
		t.Fatalf("attach: %+v %v", info, err)
	}
	if err := a.Stop(ctx, "s1"); err != nil || !stopped {
		t.Fatalf("stop: %v", err)
	}
	if err := a.Submit(ctx, "s1", "hello"); err != nil || submitted != "hello" {
		t.Fatalf("submit: %v", err)
	}
	history, err := a.History(ctx, HistoryRequest{ID: "s1", Limit: 5, Cursor: 3, SinceSeq: 2})
	if err != nil || len(history.Events) != 1 || history.Events[0].Timestamp != "2026-10-01T00:00:00Z" || history.NextCursor != 4 {
		t.Fatalf("history: %+v %v", history, err)
	}
}

func TestTetherErrorsAndInactiveAttach(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sessions/stopped" {
			_, _ = w.Write([]byte(`{"id":"stopped","state":"stopped"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"provider offline"}}`))
	}))
	defer srv.Close()
	a, err := NewTetherAdapter(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Attach(context.Background(), "stopped"); err == nil {
		t.Fatal("attached stopped session")
	}
	if _, err := a.Read(context.Background(), "missing"); err == nil {
		t.Fatal("provider error lost")
	}
	if _, err := NewTetherAdapter("invalid-address"); err == nil {
		t.Fatal("invalid transport accepted")
	}
}
